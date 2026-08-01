package ui

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pavlabs/beadsboard/internal/beads"
)

// t lands in the task list without walking tab through every field first.
func TestTJumpsToTaskList(t *testing.T) {
	m := testModel()
	require.False(t, m.focused)

	next, _ := m.handleKey(keyMsg("t"))
	m = next.(model)

	require.True(t, m.focused)
	require.Equal(t, secTasks, m.section)
	require.Equal(t, "a.1", m.currentTask(), "cursor sits on the epic's first task")
}

// Also from inside the right pane, so "enter then t" works as one motion.
func TestTJumpsFromFocusedPane(t *testing.T) {
	next, _ := testModel().handleKey(keyMsg("enter"))
	next, _ = next.(model).handleKey(keyMsg("t"))
	m := next.(model)

	require.Equal(t, secTasks, m.section)
}

// A task's detail page has no task list of its own, so t is a no-op there
// rather than focusing something that isn't shown.
func TestTIsInertOnATaskPage(t *testing.T) {
	m := testModel()
	m.taskOpen = true
	m.section = secTitle

	next, _ := m.handleKey(keyMsg("t"))
	require.Equal(t, secTitle, next.(model).section)
}

// c copies the focused field, and the bead reference when the selection is a
// bead rather than one of its fields.
func TestCopyTextBySelection(t *testing.T) {
	m := testModel()

	require.Equal(t, "a Alpha epic", m.copyText(), "epic list: reference plus title")

	m.focused = true
	for _, tc := range []struct {
		section int
		want    string
	}{
		{secTitle, "Alpha epic"},
		{secStatus, "open"},
		{secPriority, "P0"},
	} {
		m.section = tc.section
		require.Equal(t, tc.want, m.copyText())
	}

	m.section = secTasks
	require.Equal(t, "a.1 design", m.copyText(), "task list: the task under the cursor")
}

// The reference drops the project prefix but keeps the epic path, so a task id
// still names its epic: e90.4, not #4.
func TestBeadRef(t *testing.T) {
	require.Equal(t, "e90", beadRef("zoomie-e90"))
	require.Equal(t, "e90.4", beadRef("zoomie-e90.4"))
	require.Equal(t, "is6.7", beadRef("beadsboard-is6.7"))
	require.Equal(t, "plain", beadRef("plain"))
}

// A description is copied whole — it is the field most worth having on the
// clipboard, and truncating it would defeat the point.
func TestCopyKeepsWholeDescription(t *testing.T) {
	m := testModel()
	m.focused = true
	m.section = secDescription
	m.taskOpen = true
	m.taskCursor = 0

	require.Equal(t, "the design", m.copyText())
}

// o opens the bead's synced issue; a bead with no issue says so rather than
// silently doing nothing.
func TestOpenBeadNeedsASyncedIssue(t *testing.T) {
	m := testModel()
	require.NotNil(t, m.openBeadCmd())
	msg := m.openBeadCmd()().(copiedMsg)
	require.ErrorContains(t, msg.err, "no synced issue")

	issues := map[string]beads.Issue{}
	for id, is := range m.graph.Issues {
		issues[id] = is
	}
	linked := issues["a"]
	linked.ExternalRef = "https://github.com/acme/w/issues/3"
	issues["a"] = linked
	m.graph = beads.BuildGraph(issues)

	require.NotNil(t, m.openBeadCmd(), "a linked bead opens its issue")
}

// The board advertises the new keys; they are useless if undiscoverable.
func TestFooterAdvertisesNewKeys(t *testing.T) {
	f := testModel().footerLine()
	require.Contains(t, f, "t tasks")
	require.Contains(t, f, "c copy")
	require.Contains(t, f, "o issue")
}
