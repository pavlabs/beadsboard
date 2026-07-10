package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/require"

	"github.com/pavlabs/beadsboard/internal/beads"
)

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func testModel() model {
	issues := map[string]beads.Issue{
		"a":   {ID: "a", Title: "Alpha epic", IssueType: "epic", Priority: 0, Status: "open"},
		"b":   {ID: "b", Title: "Beta epic", IssueType: "epic", Priority: 1, Status: "open"},
		"a.1": {ID: "a.1", Title: "design", IssueType: "task", Status: "closed", Description: "the design", Dependencies: []beads.Dep{{DependsOnID: "a", Type: "parent-child"}}},
		"a.2": {ID: "a.2", Title: "build it", IssueType: "task", Status: "open", Labels: []string{"gate"}, Dependencies: []beads.Dep{{DependsOnID: "a", Type: "parent-child"}, {DependsOnID: "a.1", Type: "blocks"}}},
		"b.1": {ID: "b.1", Title: "ship it", IssueType: "task", Status: "open", Dependencies: []beads.Dep{{DependsOnID: "b", Type: "parent-child"}, {DependsOnID: "a.2", Type: "blocks"}}},
	}
	m := model{client: beads.NewClient("testdir"), graph: beads.BuildGraph(issues), detail: viewport.New(0, 0)}
	m.input, m.area = newInputs()
	m.width, m.height = 120, 30
	m.resizeDetail()
	m.syncDetail()
	return m
}

func TestRenderEpicsLevel(t *testing.T) {
	m := testModel()
	out := m.View()
	require.Contains(t, out, "Alpha epic")
	require.Contains(t, out, "Beta epic")
	require.Contains(t, out, "needs") // Beta needs Alpha
}

// The current epic's tasks render in the right pane's task-list region without
// any drilling; the epic's fields render above them.
func TestTasksShownForEpic(t *testing.T) {
	m := testModel()
	out := m.View()
	require.Contains(t, out, "Alpha epic") // epic fields region
	require.Contains(t, out, "build it")   // task a.2 in the task list
	require.Contains(t, out, "design")     // task a.1 in the task list
	require.Contains(t, out, "TASKS")      // task-list header
}

func TestDetailUpdatesOnHover(t *testing.T) {
	m := testModel()
	next, _ := m.handleKey(keyMsg("down"))
	m = next.(model)
	require.Contains(t, m.detail.View(), "Beta epic")
	require.Contains(t, m.detail.View(), "needs")
}

// b.1 is blocked by a.2, which lives in a different epic, so its blocker must be
// shown epic-qualified rather than as a bare "#2".
func TestCrossEpicBlockerQualified(t *testing.T) {
	m := testModel()
	require.Equal(t, []string{"a#2"}, m.blockerRefs("b.1"))
}

// e on the title section opens an inline text editor primed with the title; esc
// cancels without persisting.
func TestInlineEditTitle(t *testing.T) {
	m := testModel()
	m.focused = true
	m.section = secTitle

	next, _ := m.handleKey(keyMsg("e"))
	m = next.(model)
	require.True(t, m.editing)
	require.Equal(t, secTitle, m.editSec)
	require.Equal(t, "Alpha epic", m.input.Value())

	next, _ = m.handleKey(keyMsg("esc"))
	m = next.(model)
	require.False(t, m.editing)
}

// e on the status section cycles the valid statuses with the arrows.
func TestInlineEditStatusCycle(t *testing.T) {
	m := testModel()
	m.focused = true
	m.section = secStatus

	next, _ := m.handleKey(keyMsg("e"))
	m = next.(model)
	require.True(t, m.editing)
	require.Equal(t, 0, m.choice, "epic 'a' is open → index 0")

	next, _ = m.handleKey(keyMsg("right"))
	m = next.(model)
	require.Equal(t, "in_progress", editStatuses[m.choice])
}

// Enter commits an inline edit: it closes the editor and issues a bd update.
func TestInlineEditCommit(t *testing.T) {
	m := testModel()
	m.focused = true
	m.section = secPriority

	next, _ := m.handleKey(keyMsg("e"))
	m = next.(model)
	require.True(t, m.editing)

	next, cmd := m.handleKey(keyMsg("enter"))
	m = next.(model)
	require.False(t, m.editing, "enter commits and closes the editor")
	require.NotNil(t, cmd, "commit issues a bd update command")
}

// A long description wraps to the detail pane width instead of being clipped to
// a single line by the viewport.
func TestDescriptionWraps(t *testing.T) {
	m := testModel()
	id := m.currentEpic()
	is := m.graph.Issues[id]
	is.Description = strings.Repeat("word ", 200) // ~1000 chars, all breakable
	m.graph.Issues[id] = is
	m.syncDetail()

	require.Greater(t, m.detail.TotalLineCount(), 8, "long description spans many wrapped lines")
	for _, line := range strings.Split(m.detail.View(), "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), m.detail.Width, "no line exceeds pane width")
	}
}

// Enter focuses the right pane; Tab cycles the sections and wraps; Esc returns.
func TestFocusModelSections(t *testing.T) {
	m := testModel()
	require.False(t, m.focused)

	next, _ := m.handleKey(keyMsg("enter"))
	m = next.(model)
	require.True(t, m.focused)
	require.Equal(t, secTitle, m.section)

	for _, want := range []int{secStatus, secPriority, secDescription, secNotes, secTasks, secTitle} {
		next, _ = m.handleKey(keyMsg("tab"))
		m = next.(model)
		require.Equal(t, want, m.section)
	}

	next, _ = m.handleKey(keyMsg("esc"))
	m = next.(model)
	require.False(t, m.focused)
}

// w toggles wrap-all: a long epic title that truncates by default renders across
// multiple lines once wrap is on.
func TestWrapEpicTitles(t *testing.T) {
	m := testModel()
	long := "Substrate and infrastructure with a very long descriptive title that cannot fit"
	is := m.graph.Issues["a"]
	is.Title = long
	m.graph.Issues["a"] = is

	// Default: truncated to a single row — the tail word is not present.
	require.NotContains(t, m.View(), "cannot fit")

	next, _ := m.handleKey(keyMsg("w"))
	m = next.(model)
	require.True(t, m.wrap)
	block := m.renderEpicBlock("a", true, 40)
	require.Greater(t, len(block), 1, "long title wraps to multiple lines")
	require.Contains(t, m.View(), "cannot fit", "wrapped title shows its full text")
}

// Enter on a focused task opens its detail page (task fields, same motion as the
// epic); Esc returns to the task list.
func TestOpenTaskDetail(t *testing.T) {
	m := testModel()
	m.focused = true
	m.section = secTasks
	m.taskCursor = 0 // a.1 "design", description "the design"

	next, _ := m.handleKey(keyMsg("enter"))
	m = next.(model)
	require.True(t, m.taskOpen)
	require.Equal(t, secTitle, m.section)
	require.Contains(t, m.detail.View(), "design")     // task title
	require.Contains(t, m.detail.View(), "the design") // task description

	next, _ = m.handleKey(keyMsg("esc"))
	m = next.(model)
	require.False(t, m.taskOpen)
	require.Equal(t, secTasks, m.section)
}

// Editing inside a task's detail page targets the task, not the parent epic.
func TestTaskDetailEditTargetsTask(t *testing.T) {
	m := testModel()
	m.focused = true
	m.section = secTasks
	m.taskCursor = 0
	next, _ := m.handleKey(keyMsg("enter"))
	m = next.(model)

	next, _ = m.handleKey(keyMsg("e"))
	m = next.(model)
	require.True(t, m.editing)
	require.Equal(t, "design", m.input.Value(), "edits the task title, not the epic's")
}

// Tab in a task's detail page cycles the five fields and wraps, never reaching
// the task-list section (a task has no subtasks).
func TestTaskDetailTabCycle(t *testing.T) {
	m := testModel()
	m.focused = true
	m.section = secTasks
	m.taskCursor = 0
	next, _ := m.handleKey(keyMsg("enter"))
	m = next.(model)
	require.Equal(t, secTitle, m.section)

	for _, want := range []int{secStatus, secPriority, secDescription, secNotes, secTitle} {
		next, _ = m.handleKey(keyMsg("tab"))
		m = next.(model)
		require.Equal(t, want, m.section)
		require.NotEqual(t, secTasks, m.section)
	}
}

// With the task-list section focused, the hovered task's fields preview splits in
// beside the list.
func TestTaskPreviewBesideList(t *testing.T) {
	m := testModel()
	m.focused = true
	m.section = secTasks
	m.taskCursor = 0 // a.1 has description "the design"
	require.Contains(t, m.View(), "the design", "hovered task preview renders beside the list")
}

// When the task-list section is focused, up/down move the task cursor.
func TestTaskSectionCursor(t *testing.T) {
	m := testModel() // epic "a" has tasks a.1, a.2
	m.focused = true
	m.section = secTasks
	require.Equal(t, 0, m.taskCursor)

	next, _ := m.handleKey(keyMsg("down"))
	m = next.(model)
	require.Equal(t, 1, m.taskCursor)

	next, _ = m.handleKey(keyMsg("down"))
	m = next.(model)
	require.Equal(t, 1, m.taskCursor, "clamps at the last task")
}
