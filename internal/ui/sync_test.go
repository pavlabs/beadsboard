package ui

import (
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

// Only the named beads are pushed, and a bead whose repo cannot be resolved
// contributes no work rather than an empty group.
func TestPushGroupsCmdScope(t *testing.T) {
	m := syncModel()

	require.Nil(t, m.pushGroupsCmd(nil), "nothing to push means no command")
	require.Nil(t, m.pushGroupsCmd([]string{"nonexistent"}), "unresolvable bead pushes nothing")
	require.NotNil(t, m.pushGroupsCmd([]string{"a"}))
}

// The deadline scales with the batch, so a one-bead edit is not held to the same
// ceiling as a first-time sync of a whole epic.
func TestSyncTimeoutScalesWithBatch(t *testing.T) {
	one, many := syncTimeout(1), syncTimeout(27)

	require.Greater(t, many, one)
	require.GreaterOrEqual(t, one, 15*time.Second, "room for bd's Dolt cold start")
	require.Greater(t, many, 90*time.Second, "a 27-bead repo needs more than the old shared ceiling")
}
