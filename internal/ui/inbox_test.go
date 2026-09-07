package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pavlabs/beadsboard/internal/beads"
)

// blockedModel gives the board something to be worried about: b.1 is marked
// blocked, so it becomes one attention item with no agent involved.
func blockedModel() model {
	m := testModel()
	is := m.graph.Issues["b.1"]
	is.Status = "blocked"
	issues := map[string]beads.Issue{}
	for id, v := range m.graph.Issues {
		issues[id] = v
	}
	issues["b.1"] = is
	m.graph = beads.BuildGraph(issues)
	m.syncDetail()
	return m
}

// The badge is the board-wide signal, so it rides the header whatever pane is up.
func TestHeaderShowsAttentionBadge(t *testing.T) {
	require.NotContains(t, testModel().View(), "need attention")
	require.Contains(t, blockedModel().View(), "1 need attention")
}

// i opens the inbox from the board, listing items with their reason.
func TestInboxOpensAndListsItems(t *testing.T) {
	next, _ := blockedModel().handleKey(keyMsg("i"))
	m := next.(model)

	require.True(t, m.inboxOpen)
	out := m.View()
	require.Contains(t, out, "ATTENTION")
	require.Contains(t, out, "blocked")
	require.Contains(t, out, "b.1")
	require.Contains(t, out, "jump to bead") // footer switches to inbox keys
}

// An idle board says so rather than showing an empty box.
func TestInboxEmptyState(t *testing.T) {
	next, _ := testModel().handleKey(keyMsg("i"))
	m := next.(model)

	require.Contains(t, m.View(), "nothing needs you")
}

// Enter opens the actual task, with its epic selected and the overlay closed.
func TestInboxJumpsToBead(t *testing.T) {
	next, _ := blockedModel().handleKey(keyMsg("i"))
	next, _ = next.(model).handleKey(keyMsg("enter"))
	m := next.(model)

	require.False(t, m.inboxOpen)
	require.Equal(t, "b", m.graph.Epics[m.epicCursor])
	require.Equal(t, "b.1", m.currentTask())
	require.True(t, m.focused)
	require.True(t, m.taskOpen)
	require.Equal(t, "b.1", m.target())
	require.Equal(t, secTitle, m.section)
}

// esc closes without moving the board's cursor.
func TestInboxEscCloses(t *testing.T) {
	start := blockedModel()
	next, _ := start.handleKey(keyMsg("i"))
	next, _ = next.(model).handleKey(keyMsg("esc"))
	m := next.(model)

	require.False(t, m.inboxOpen)
	require.Equal(t, start.epicCursor, m.epicCursor)
}

// While the inbox has the keys, board shortcuts must not fire underneath it —
// q would otherwise quit instead of closing the overlay.
func TestInboxCapturesKeys(t *testing.T) {
	next, _ := blockedModel().handleKey(keyMsg("i"))
	next, cmd := next.(model).handleKey(keyMsg("q"))
	m := next.(model)

	require.False(t, m.inboxOpen)
	require.Nil(t, cmd)
}

// The cursor cannot walk off either end of the list.
func TestInboxCursorClamped(t *testing.T) {
	next, _ := blockedModel().handleKey(keyMsg("i"))
	m := next.(model)
	for range 3 {
		n, _ := m.handleKey(keyMsg("down"))
		m = n.(model)
	}
	require.Equal(t, 0, m.inboxCursor) // only one item

	for range 3 {
		n, _ := m.handleKey(keyMsg("up"))
		m = n.(model)
	}
	require.Equal(t, 0, m.inboxCursor)
}

// The badge hides while the inbox is open — the list itself is the signal.
func TestBadgeHiddenWhileInboxOpen(t *testing.T) {
	next, _ := blockedModel().handleKey(keyMsg("i"))
	m := next.(model)

	require.False(t, strings.Contains(m.View(), "need attention"))
}

// A pull request shows in the same inbox as agent items, labelled with why it
// needs a human and the bead it maps to.
func TestInboxShowsPullRequests(t *testing.T) {
	m := testModel()
	m.pulls = []beads.PullRequest{
		{Repo: "acme/proto", Number: 8, Title: "add job-spec", Branch: "beadsboard/a.2", Checks: "FAILURE", Updated: time.Now()},
	}
	next, _ := m.handleKey(keyMsg("i"))
	m = next.(model)

	out := m.View()
	require.Contains(t, out, "checks red")
	require.Contains(t, out, "a.2") // correlated to its bead, not shown as proto#8
	require.Contains(t, out, "add job-spec")
}

// A PR that maps to no bead is still listed, under its own ref.
func TestInboxShowsUncorrelatedPullRequest(t *testing.T) {
	m := testModel()
	m.pulls = []beads.PullRequest{
		{Repo: "acme/proto", Number: 9, Title: "chore: bump deps", Branch: "chore/deps", Updated: time.Now()},
	}
	next, _ := m.handleKey(keyMsg("i"))
	m = next.(model)

	require.Contains(t, m.View(), "proto#9")
}

// The PR fetch is rate-limit sensitive, so it stays quiet unless GitHub sync is
// on, and it does not re-fire inside its interval.
func TestPullsFetchGating(t *testing.T) {
	m := testModel()
	require.Nil(t, m.pullsCmd(), "no fetch without github sync")

	m.cfg.GitHubSync = true
	m.cfg.GitHubRepository = "acme/widgets"
	require.NotNil(t, m.pullsCmd(), "fetch when configured and never fetched")

	m.pullsAt = time.Now()
	require.Nil(t, m.pullsCmd(), "no refetch inside the interval")

	m.pullsAt = time.Now().Add(-2 * pullsInterval)
	require.NotNil(t, m.pullsCmd(), "refetch once the interval elapses")
}

// Resolving the board's repos can shell out to git, so it happens inside the
// command rather than on the update goroutine that renders the next frame. A
// board with no resolvable repo therefore reports an empty result rather than
// skipping the command — and must not reach GitHub to discover that.
func TestPullsFetchWithoutResolvableRepoIsEmpty(t *testing.T) {
	m := testModel()
	m.cfg.GitHubSync = true // no GitHubRepository set, and no repo:: labels

	cmd := m.pullsCmd()
	require.NotNil(t, cmd)
	require.Equal(t, pullsLoadedMsg{}, cmd())
}

// A GitHub failure stamps the clock anyway, so an outage cannot become a retry
// loop against a rate-limited API.
func TestPullsErrorStampsClock(t *testing.T) {
	m := testModel()
	next, _ := m.Update(pullsLoadedMsg{err: errors.New("gh api: rate limited")})
	m = next.(model)

	require.False(t, m.pullsAt.IsZero())
	require.Contains(t, m.notice, "rate limited")
}

// A settled board states it outright, so the absence of a spinner is never left
// to be read as "maybe still working". The wording tracks what settling actually
// guaranteed: with sync on, the GitHub push finished too.
func TestHeaderShowsSyncedState(t *testing.T) {
	m := testModel()
	require.Contains(t, m.View(), "✓ synced")
	require.NotContains(t, m.View(), "synced with github")

	m.cfg.GitHubSync = true
	require.Contains(t, m.View(), "✓ synced with github")

	m.loading = true
	require.Contains(t, m.View(), "refreshing")
	require.NotContains(t, m.View(), "✓ synced")

	m.loading = false
	m.err = errors.New("bd export: boom")
	out := m.View()
	require.Contains(t, out, "boom")
	require.NotContains(t, out, "✓ synced", "an errored load is not a synced board")
}

// The jump has to resolve against the visible list, since that is what the
// cursors index — with a search filter active, a graph-order index selects the
// wrong epic.
func TestInboxJumpRespectsSearchFilter(t *testing.T) {
	m := blockedModel()
	m.searchScope = scopeEpics
	m.search.SetValue("beta") // filters the epic list down to "Beta epic" (id b)

	next, _ := m.jumpToBead("b.1")
	m = next.(model)

	require.Equal(t, "b", m.currentEpic())
	require.Equal(t, "b.1", m.currentTask())
}
