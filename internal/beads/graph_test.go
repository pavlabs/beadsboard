package beads

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Two epics: A blocks B via a cross-epic task dependency. Within A, task A.2 is
// blocked by A.1; A.1 is closed so A.2 is ready. B.1 depends on A.2 (open) so B.1
// is blocked and epic B "needs" epic A.
func fixture() map[string]Issue {
	return map[string]Issue{
		"a":   {ID: "a", Title: "Alpha", IssueType: "epic", Priority: 0, Status: "open"},
		"b":   {ID: "b", Title: "Beta", IssueType: "epic", Priority: 1, Status: "open"},
		"a.1": {ID: "a.1", Title: "design", IssueType: "task", Status: "closed", Dependencies: []Dep{{DependsOnID: "a", Type: "parent-child"}}},
		"a.2": {
			ID: "a.2", Title: "build", IssueType: "task", Status: "open",
			Dependencies: []Dep{{DependsOnID: "a", Type: "parent-child"}, {DependsOnID: "a.1", Type: "blocks"}},
		},
		"b.1": {
			ID: "b.1", Title: "ship", IssueType: "task", Status: "open",
			Dependencies: []Dep{{DependsOnID: "b", Type: "parent-child"}, {DependsOnID: "a.2", Type: "blocks"}},
		},
	}
}

func TestGraphStatuses(t *testing.T) {
	g := BuildGraph(fixture())

	require.Equal(t, StatusDone, g.TaskStatus["a.1"])
	require.Equal(t, StatusReady, g.TaskStatus["a.2"], "a.2's only blocker a.1 is closed")
	require.Equal(t, StatusBlocked, g.TaskStatus["b.1"], "b.1 waits on open a.2")

	require.Equal(t, []string{"a.2"}, g.OpenBlockerRefs("b.1"))
	require.Empty(t, g.OpenBlockerRefs("a.2"))
}

func TestGraphTopoOrderWithinEpic(t *testing.T) {
	g := BuildGraph(fixture())
	require.Equal(t, []string{"a.1", "a.2"}, g.Tasks["a"], "prerequisite lists before what it unblocks")
}

func TestEpicDAGAndOrder(t *testing.T) {
	g := BuildGraph(fixture())

	require.Equal(t, []string{"a"}, g.Prereqs["b"], "B needs A")
	require.Empty(t, g.Prereqs["a"])
	require.Equal(t, []string{"b"}, g.Unlocks["a"])

	// A (P0, no prereqs) sorts before B (P1, needs A).
	require.Equal(t, []string{"a", "b"}, g.Epics)
}

func TestOrphanTasksSurface(t *testing.T) {
	iss := fixture()
	// A task whose epic can't be resolved (no parent-child edge, no "<epic>.N").
	iss["loose"] = Issue{ID: "loose", Title: "stray", IssueType: "task", Status: "open"}

	g := BuildGraph(iss)

	require.Contains(t, g.Epics, orphanEpicID)
	require.Equal(t, []string{"loose"}, g.Tasks[orphanEpicID])
	require.Equal(t, orphanEpicID, g.Epics[len(g.Epics)-1], "orphan bucket sorts last")
	require.Equal(t, StatusReady, g.TaskStatus["loose"], "orphan tasks still get a status")
}

func TestSanitizeStripsEscapes(t *testing.T) {
	require.Equal(t, "clean", sanitize("clean"))
	// Removing ESC/BEL neutralizes the OSC 52 clipboard sequence; the leftover
	// payload is inert printable text.
	got := sanitize("hel\x1b]52;c;pwn\x07lo")
	require.NotContains(t, got, "\x1b")
	require.NotContains(t, got, "\x07")
	require.Equal(t, "a\nb\tc", sanitize("a\nb\tc"), "newlines and tabs preserved")
}

func TestEpicProgressAndStatus(t *testing.T) {
	g := BuildGraph(fixture())

	done, total := g.EpicProgress("a")
	require.Equal(t, 1, done)
	require.Equal(t, 2, total)

	require.Equal(t, StatusOpen, g.EpicStatus["a"], "a has an open child")
}
