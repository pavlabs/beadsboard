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

// Pressing 'e' opens the modal field picker (default: description) without
// launching anything; tab cycles description→notes→title; enter launches the
// editor on the chosen field and closes the picker.
func TestEditFieldPicker(t *testing.T) {
	m := testModel()

	next, cmd := m.handleKey(keyMsg("e"))
	m = next.(model)
	require.True(t, m.editing)
	require.Nil(t, cmd, "opening the picker launches nothing yet")
	require.Equal(t, fieldDescription, m.editField)
	require.Contains(t, m.View(), "description")

	next, _ = m.handleKey(keyMsg("tab"))
	m = next.(model)
	require.Equal(t, fieldNotes, m.editField)
	next, _ = m.handleKey(keyMsg("tab"))
	m = next.(model)
	require.Equal(t, fieldTitle, m.editField, "tab wraps back to the first field")

	next, cmd = m.handleKey(keyMsg("enter"))
	m = next.(model)
	require.False(t, m.editing)
	require.NotNil(t, cmd, "enter launches the editor")
	require.Equal(t, 0, m.epicCursor, "picker doesn't disturb navigation")
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

// Esc cancels the picker without launching an editor.
func TestEditPickerCancel(t *testing.T) {
	m := testModel()
	next, _ := m.handleKey(keyMsg("e"))
	m = next.(model)
	next, cmd := m.handleKey(keyMsg("esc"))
	m = next.(model)
	require.False(t, m.editing)
	require.Nil(t, cmd)
}
