package ui

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pavlabs/beadsboard/internal/beads"
)

// priorityGroupModel builds a graph with two priority levels split across two
// epics, so a flattened bucket must interleave an epic with another epic's
// task rather than just listing one epic's own tasks.
func priorityGroupModel() model {
	m := testModel()
	m.graph = beads.BuildGraph(map[string]beads.Issue{
		"a":   {ID: "a", Title: "Alpha epic", IssueType: "epic", Priority: 0, Status: "open"},
		"a.1": {ID: "a.1", Title: "alpha task", IssueType: "task", Priority: 0, Status: "open"},
		"a.2": {ID: "a.2", Title: "alpha urgent", IssueType: "task", Priority: 2, Status: "open"},
		"b":   {ID: "b", Title: "Beta epic", IssueType: "epic", Priority: 2, Status: "open"},
	})
	m.clampCursors()
	m.syncDetail()
	return m
}

func TestGroupModeTogglesBetweenEpicAndPriority(t *testing.T) {
	m := testModel()
	require.Equal(t, groupByEpic, m.groupMode, "default grouping is the epic hierarchy")
	require.Equal(t, m.graph.Epics, m.visibleEpics())

	m = press(m, "P")
	require.Equal(t, groupByPriority, m.groupMode)
	require.NotEqual(t, m.graph.Epics, m.visibleEpics(), "priority mode replaces the epic list with buckets")

	m = press(m, "O")
	require.Equal(t, groupByEpic, m.groupMode, "O restores the default grouping")
	require.Equal(t, m.graph.Epics, m.visibleEpics())
}

func TestPriorityGroupingFlattensEpicsAndTasksByLevel(t *testing.T) {
	m := priorityGroupModel()
	m = press(m, "P")

	require.Equal(t, []string{priorityGroupID(0), priorityGroupID(2)}, m.visibleEpics(),
		"only the two priority levels actually in use appear, most urgent first")

	m.epicCursor = 0
	require.Equal(t, []string{"a", "a.1"}, m.currentEpicTasks(), "P0 bucket holds the epic and its P0 task")

	m.epicCursor = 1
	require.Equal(t, []string{"a.2", "b"}, m.currentEpicTasks(), "P2 bucket mixes a task from epic a with epic b")
}

func TestPriorityGroupingDisablesEpicOnlyActions(t *testing.T) {
	m := priorityGroupModel()
	m = press(m, "P")

	require.Equal(t, "", m.currentEpic(), "no single real epic is selected while grouping by priority")

	before := m.pendingDelete
	m = press(m, "d")
	require.Equal(t, before, m.pendingDelete, "left-pane delete on a bucket must no-op")

	m = press(m, "D")
	require.False(t, m.pickerOpen, "subtree dispatch needs a real epic and must no-op on a bucket")
}

func TestPriorityGroupingSurfacesEpicsInTheFlatList(t *testing.T) {
	m := priorityGroupModel()
	m = press(m, "P")
	m.epicCursor = indexOf(m.visibleEpics(), priorityGroupID(2))
	m.clampCursors()
	m.syncDetail()

	require.Contains(t, m.visibleTasks(), "b", "epic b has priority 2 and belongs in that bucket's flat list")

	out := m.View()
	require.Contains(t, out, "ITEMS", "the flat view relabels the list since it isn't only tasks")
	require.Contains(t, out, "Beta epic", "the epic itself renders in the flattened list")
}
