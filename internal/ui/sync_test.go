package ui

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pavlabs/beadsboard/internal/beads"
)

// syncModel is a board whose beads all have issues, so a push is only ever
// triggered by a real change rather than by a first-time create.
func syncModel() model {
	m := testModel()
	issues := map[string]beads.Issue{}
	for id, is := range m.graph.Issues {
		is.ExternalRef = "https://github.com/acme/w/issues/1"
		issues[id] = is
	}
	m.graph = beads.BuildGraph(issues)
	m.cfg.GitHubSync = true
	m.cfg.GitHubRepository = "acme/widgets"
	m.syncDetail()
	return m
}

// Adopting an identical load issues no push at all: the board used to resync
// every bead after every load, which made one edit cost minutes.
func TestAdoptWithoutChangesDoesNotPush(t *testing.T) {
	m := syncModel()
	next, cmd := m.adopt(m.graph, 99)

	require.Nil(t, cmd)
	require.False(t, next.(model).loading)
}

// A changed bead does push, and holds the loading flag while it runs so the
// watcher cannot fire a concurrent sync.
func TestAdoptWithChangePushes(t *testing.T) {
	m := syncModel()
	issues := map[string]beads.Issue{}
	for id, is := range m.graph.Issues {
		issues[id] = is
	}
	edited := issues["a"]
	edited.Title = "Alpha epic, renamed"
	issues["a"] = edited

	next, cmd := m.adopt(beads.BuildGraph(issues), 100)

	require.NotNil(t, cmd)
	require.True(t, next.(model).loading)
}

// With sync off, a change is adopted without any GitHub work.
func TestAdoptWithSyncOffNeverPushes(t *testing.T) {
	m := syncModel()
	m.cfg.GitHubSync = false

	next, cmd := m.adopt(beads.BuildGraph(nil), 101)

	require.Nil(t, cmd)
	require.False(t, next.(model).loading)
}

// Nothing to push means no command at all. Resolving a bead's repo can shell out
// to git, so that happens inside the command rather than on the goroutine that
// renders the next frame — which means a bead resolving to no repo reports an
// empty push instead of skipping the command, without reaching GitHub.
func TestPushGroupsCmdScope(t *testing.T) {
	m := syncModel()
	require.Nil(t, m.pushGroupsCmd(nil), "nothing to push means no command")
	require.NotNil(t, m.pushGroupsCmd([]string{"a"}))

	noRepo := syncModel()
	noRepo.cfg.GitHubRepository = "" // no default, and no repo:: labels either
	cmd := noRepo.pushGroupsCmd([]string{"a"})
	require.NotNil(t, cmd)
	require.Equal(t, pushedMsg{}, cmd())

	// A bead absent from the board is dropped rather than resolved to the default.
	cmd = m.pushGroupsCmd([]string{"nonexistent"})
	require.NotNil(t, cmd)
	require.Equal(t, pushedMsg{}, cmd())
}

// A bead is not pushed twice for the same content. This is the backstop against a
// push loop: if bd writes a mirrored field back during its own push, the graph
// diff alone would read that as a fresh change and push again, forever.
func TestUnpushedSkipsAlreadyPushedContent(t *testing.T) {
	m := syncModel()
	digest := beads.SyncDigest(m.graph.Issues["a"])

	require.Equal(t, []string{"a"}, m.unpushed([]string{"a"}), "never pushed")

	m.pushed = map[string]uint64{"a": digest}
	require.Empty(t, m.unpushed([]string{"a"}), "same content as the last push")

	m.pushed = map[string]uint64{"a": digest + 1}
	require.Equal(t, []string{"a"}, m.unpushed([]string{"a"}), "content moved since")
}

// Digests are recorded only for beads that actually reached GitHub, so one repo
// failing partway does not mark the rest as pushed.
func TestPushedDigestsRecordedForLandedIdsOnly(t *testing.T) {
	m := syncModel()
	next, _ := m.Update(pushedMsg{ids: []string{"a"}, err: errors.New("gh: boom")})
	m = next.(model)

	require.Contains(t, m.pushed, "a")
	require.NotContains(t, m.pushed, "b")
	require.Contains(t, m.notice, "boom")
}

// A poll whose export finished after a newer load landed must not install its
// stale graph: it would revert the edit on screen and push the bead again.
func TestStalePollDiscarded(t *testing.T) {
	m := syncModel()
	m.rev, m.loadGen = 1, 7
	before := m.graph

	next, cmd := m.Update(polledMsg{graph: beads.BuildGraph(nil), rev: 2, gen: 6})
	m = next.(model)

	require.Nil(t, cmd)
	require.Same(t, before, m.graph)
	require.Equal(t, uint64(1), m.rev)
}

// A failing poll is surfaced, because the poll is the load now: silence would
// leave the board frozen on stale data while the header claims it is synced.
func TestPollErrorSurfaces(t *testing.T) {
	m := syncModel()
	next, _ := m.Update(polledMsg{err: errors.New("bd export: boom"), gen: m.loadGen})
	m = next.(model)

	require.Error(t, m.err)
	require.NotContains(t, m.View(), "✓ synced")
}

// The deadline scales with the batch, so a one-bead edit is not held to the same
// ceiling as a first-time sync of a whole epic.
func TestSyncTimeoutScalesWithBatch(t *testing.T) {
	one, many := syncTimeout(1), syncTimeout(27)

	require.Greater(t, many, one)
	require.GreaterOrEqual(t, one, 15*time.Second, "room for bd's Dolt cold start")
	require.Greater(t, many, 90*time.Second, "a 27-bead repo needs more than the old shared ceiling")
}
