package ui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	"github.com/pavlabs/beadsboard/internal/beads"
)

func batchLen(t *testing.T, cmd tea.Cmd) int {
	t.Helper()
	msg, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	return len(msg)
}

// An unchanged poll is dropped. The board used to watch .beads file state, which
// its own `bd` reads churned, so every tick looked like an external write and
// reloaded forever. Polling by revision means an idle board stays idle.
func TestPollWithSameRevisionDoesNotReload(t *testing.T) {
	m := testModel()
	m.rev = 42
	before := m.graph

	next, cmd := m.Update(polledMsg{graph: beads.BuildGraph(nil), rev: 42})
	m = next.(model)

	require.Nil(t, cmd)
	require.False(t, m.loading)
	require.Same(t, before, m.graph)
}

// A real change is adopted straight from the poll — no second load, and the
// loading state never flashes, because the poll already carries the new graph.
func TestPollWithNewRevisionAdoptsGraph(t *testing.T) {
	m := testModel()
	m.rev = 42
	fresh := beads.BuildGraph(map[string]beads.Issue{
		"z": {ID: "z", Title: "Zeta epic", IssueType: "epic", Status: "open"},
	})

	next, _ := m.Update(polledMsg{graph: fresh, rev: 43})
	m = next.(model)

	require.Same(t, fresh, m.graph)
	require.Equal(t, uint64(43), m.rev)
	require.False(t, m.loading)
	require.Contains(t, m.View(), "Zeta epic")
}

// A poll landing mid-load is discarded rather than racing the load it overlaps.
func TestPollIgnoredWhileLoading(t *testing.T) {
	m := testModel()
	m.rev = 42
	m.loading = true
	before := m.graph

	next, cmd := m.Update(polledMsg{graph: beads.BuildGraph(nil), rev: 99})
	m = next.(model)

	require.Nil(t, cmd)
	require.Same(t, before, m.graph)
	require.Equal(t, uint64(42), m.rev)
}

// Tick must preserve the comments gate mutation through Update's value
// receiver. Calling refreshCommentsCmd directly would miss a regression where
// Update accidentally invokes it on a discarded copy.
func TestTickGatesCommentsAfterFirstFetch(t *testing.T) {
	m := testModel()
	m.rev = 42
	m.commentBead = m.target()

	next, cmd := m.Update(tickMsg{})
	m = next.(model)

	require.Equal(t, 3, batchLen(t, cmd), "timer, comments fetch, and poll")
	require.Equal(t, uint64(42), m.commentsRev)
	require.True(t, m.commentsFresh)

	next, cmd = m.Update(tickMsg{})
	m = next.(model)
	require.Equal(t, 2, batchLen(t, cmd), "an idle tick runs only the timer and poll")
	require.Equal(t, uint64(42), m.commentsRev, "an idle tick keeps the successful gate")
}

func TestCommentsRefetchedWhenTargetOrRevisionChanges(t *testing.T) {
	m := testModel()
	m.rev, m.commentsRev = 43, 42
	m.commentBead = m.target()
	m.commentsFresh = true

	next, cmd := m.Update(tickMsg{})
	m = next.(model)
	require.Equal(t, 3, batchLen(t, cmd))
	require.Equal(t, uint64(43), m.commentsRev)

	m.commentBead, m.commentsRev = "another-bead", m.rev
	m.commentsFresh = true
	next, cmd = m.Update(tickMsg{})
	m = next.(model)
	require.Equal(t, 3, batchLen(t, cmd), "target change issues a new fetch")
}

func TestFailedCommentsFetchPreservesCacheAndRetries(t *testing.T) {
	m := testModel()
	m.rev, m.commentsRev = 42, 42
	m.commentBead = m.target()
	m.commentsFresh = true
	m.comments = []beads.Comment{{Text: "last good activity"}}

	next, _ := m.Update(commentsLoadedMsg{bead: m.target(), rev: 42, err: errors.New("bd busy")})
	m = next.(model)

	require.Equal(t, "last good activity", m.comments[0].Text)
	require.False(t, m.commentsFresh)
	next, _ = m.Update(tickMsg{})
	require.Equal(t, uint64(42), next.(model).commentsRev, "the next tick retries")
}

// The first poll of a session has no baseline to compare against, so it adopts.
func TestFirstPollAdoptsWithoutBaseline(t *testing.T) {
	m := testModel()
	m.rev = 0 // nothing loaded yet
	fresh := beads.BuildGraph(nil)

	next, _ := m.Update(polledMsg{graph: fresh, rev: 7})
	m = next.(model)

	require.Equal(t, uint64(7), m.rev)
	require.Same(t, fresh, m.graph)
}
