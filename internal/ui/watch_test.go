package ui

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pavlabs/beadsboard/internal/beads"
)

// cachedCommentsModel is a board whose timeline cache was just fetched for the
// bead under the cursor, i.e. one with no reason to fetch again.
func cachedCommentsModel() model {
	m := testModel()
	m.rev = 42
	m.commentBead, m.commentsRev, m.commentsAt = m.target(), m.rev, time.Now()
	return m
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

// An idle board fetches no timeline. `bd comments` is a whole second bd process
// contending with the poll's export for the same Dolt engine, which used to burn
// a third of a core continuously on a board where nothing was happening.
func TestCommentsNotRefetchedWhileNothingChanged(t *testing.T) {
	m := cachedCommentsModel()

	require.Nil(t, m.refreshCommentsCmd())
}

// Moving to another bead must show that bead's timeline, not the cached one.
func TestCommentsRefetchedWhenTargetChanges(t *testing.T) {
	m := cachedCommentsModel()
	m.commentBead = "some other bead"

	require.NotNil(t, m.refreshCommentsCmd())
}

// A poll that adopted a new revision means the beads themselves moved, so the
// timeline is refetched with them.
func TestCommentsRefetchedAfterRevisionMoves(t *testing.T) {
	m := cachedCommentsModel()
	m.rev = 43 // a poll adopted new data

	cmd := m.refreshCommentsCmd()

	require.NotNil(t, cmd)
	require.Equal(t, uint64(43), m.commentsRev, "the stamp follows the revision it fetched at")
}

// The fallback cadence is the only thing that surfaces an agent's comment:
// comments are not part of the export revision, so posting one moves neither the
// target nor the revision and the two gates above would hold forever.
func TestCommentsRefetchedOnFallbackCadence(t *testing.T) {
	m := cachedCommentsModel()
	m.commentsAt = time.Now().Add(-commentsInterval - time.Second)

	before := m.commentsAt
	require.NotNil(t, m.refreshCommentsCmd())
	require.True(t, m.commentsAt.After(before), "the cadence restarts from the reissued fetch")
}

// With no bead under the cursor there is no timeline to fetch, and the empty
// board must not stamp a cache it never filled.
func TestCommentsSkippedWithoutTarget(t *testing.T) {
	m := testModel()
	m.graph = beads.BuildGraph(nil)

	require.Nil(t, m.refreshCommentsCmd())
	require.True(t, m.commentsAt.IsZero())
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
