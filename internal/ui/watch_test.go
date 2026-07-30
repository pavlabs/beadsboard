package ui

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pavlabs/beadsboard/internal/beads"
)

// An unchanged poll is dropped. The board used to watch .beads file state, which
// its own `bd` reads churned, so every tick looked like an external write and
// reloaded forever. Polling by revision means an idle board stays idle.
func TestPollWithSameRevisionDoesNotReload(t *testing.T) {
	m := testModel()
	m.rev, m.hasRev = 42, true
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
	m.rev, m.hasRev = 42, true
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
	m.rev, m.hasRev = 42, true
	m.loading = true
	before := m.graph

	next, cmd := m.Update(polledMsg{graph: beads.BuildGraph(nil), rev: 99})
	m = next.(model)

	require.Nil(t, cmd)
	require.Same(t, before, m.graph)
	require.Equal(t, uint64(42), m.rev)
}

// The first poll of a session has no baseline to compare against, so it adopts.
func TestFirstPollAdoptsWithoutBaseline(t *testing.T) {
	m := testModel()
	m.hasRev = false
	fresh := beads.BuildGraph(nil)

	next, _ := m.Update(polledMsg{graph: fresh, rev: 7})
	m = next.(model)

	require.True(t, m.hasRev)
	require.Equal(t, uint64(7), m.rev)
	require.Same(t, fresh, m.graph)
}
