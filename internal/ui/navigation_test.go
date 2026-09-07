package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/require"

	"github.com/pavlabs/beadsboard/internal/beads"
	"github.com/pavlabs/beadsboard/internal/usage"
)

func press(m model, keys ...string) model {
	for _, key := range keys {
		next, _ := m.handleKey(keyMsg(key))
		m = next.(model)
	}
	return m
}

func TestTaskFiltersComposeAndDoNotChangeGraph(t *testing.T) {
	m := testModel()
	m.graph.Issues["a.3"] = beads.Issue{ID: "a.3", Title: "build active", Status: "in_progress", IssueType: "task"}
	m.graph = beads.BuildGraph(m.graph.Issues)
	all := append([]string(nil), m.graph.Tasks["a"]...)
	m = press(m, "t", "O")
	require.Equal(t, []string{"a.3", "a.2"}, m.visibleTasks())
	require.Equal(t, all, m.graph.Tasks["a"])
	done, total := m.graph.EpicProgress("a")
	require.Equal(t, 1, done)
	require.Equal(t, 3, total)
	m.searchScope = scopeTasks
	m.search.SetValue("build")
	require.Equal(t, []string{"a.3", "a.2"}, m.visibleTasks())
	m = press(m, "C")
	require.Empty(t, m.visibleTasks())
	m.clearSearch()
	require.Equal(t, []string{"a.1"}, m.visibleTasks())
	m = press(m, "A")
	require.Equal(t, all, m.visibleTasks())
	require.Equal(t, tabDetails, m.tab, "A is scoped to the task list")
}

func TestFuzzySearchFindsTitlesAndFullOrShortIDs(t *testing.T) {
	m := testModel()
	m.graph = beads.BuildGraph(map[string]beads.Issue{
		"board-zjh":   {ID: "board-zjh", Title: "Navigation", IssueType: "epic", Status: "open"},
		"board-abc":   {ID: "board-abc", Title: "Operations", IssueType: "epic", Status: "open"},
		"board-zjh.1": {ID: "board-zjh.1", Title: "Quota dashboard", IssueType: "task", Status: "open"},
		"board-zjh.2": {ID: "board-zjh.2", Title: "Fullscreen", IssueType: "task", Status: "closed"},
	})
	for _, query := range []string{"BOARD-ZJH", "zjh", "nvgt"} {
		m.searchScope = scopeEpics
		m.search.SetValue(query)
		require.Equal(t, []string{"board-zjh"}, m.visibleEpics(), query)
	}
	m.clearSearch()
	m.epicCursor = indexOf(m.visibleEpics(), "board-zjh")
	for _, query := range []string{"board-zjh.1", "ZJH.1", "#1", "qtadsh"} {
		m.searchScope = scopeTasks
		m.search.SetValue(query)
		require.Equal(t, []string{"board-zjh.1"}, m.visibleTasks(), query)
	}
	m.taskFilter = tasksClosed
	m.search.SetValue("#1")
	require.Empty(t, m.visibleTasks(), "ID search must respect status filter")
	m.search.SetValue("#2")
	require.Equal(t, []string{"board-zjh.2"}, m.visibleTasks())
	ids := []string{"board-other", "board-zjh"}
	got := fuzzyFilter(ids, "board-zjh", func(string) string { return "board-zjh related" })
	require.Equal(t, "board-zjh", got[0], "exact ID beats a title containing the ID")
}

func TestDashboardKeysDuringInitialHydration(t *testing.T) {
	m := testModel()
	m.graph = nil
	require.NotPanics(t, func() { m = press(m, "v", "down", "end", "esc") })
}

func TestEmptyTaskFilterCanBeRecovered(t *testing.T) {
	m := testModel()
	m.epicCursor = 1
	m.taskFilter = tasksClosed
	m = press(m, "t")
	require.True(t, m.focused)
	require.Contains(t, m.taskListContent(80, 10), "A shows all")
	m = press(m, "A")
	require.Equal(t, "b.1", m.currentTask())
}

func TestInboxRevealsHiddenAndOrphanTasks(t *testing.T) {
	for _, tc := range []struct {
		name, target string
		scope        int
		query        string
	}{{"epic search", "b.1", scopeEpics, "alpha"}, {"task search", "b.1", scopeTasks, "design"}, {"orphan", "loose", scopeEpics, "alpha"}} {
		t.Run(tc.name, func(t *testing.T) {
			m := testModel()
			m.graph.Issues["loose"] = beads.Issue{ID: "loose", Title: "orphan blocked", Status: "blocked", IssueType: "task"}
			m.graph = beads.BuildGraph(m.graph.Issues)
			m.taskFilter = tasksClosed
			m.searchScope = tc.scope
			m.search.SetValue(tc.query)
			next, _ := m.jumpToBead(tc.target)
			m = next.(model)
			require.Equal(t, tc.target, m.target())
			require.True(t, m.taskOpen)
			require.False(t, m.inboxOpen)
		})
	}
	m := testModel()
	next, _ := m.jumpToBead("missing")
	require.Contains(t, next.(model).notice, "no longer exists")
}

func TestFullscreenRestoresListAndDetail(t *testing.T) {
	for _, fromTask := range []bool{false, true} {
		m := press(testModel(), "t")
		if fromTask {
			m = press(m, "enter")
			m.section = secDescription
		}
		section := m.section
		selected := m.currentTask()
		m = press(m, "f")
		require.True(t, m.fullscreen)
		require.True(t, m.taskOpen)
		require.Equal(t, m.width-4, m.detail.Width)
		require.NotContains(t, m.panes(), "Beta epic")
		next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		m = next.(model)
		require.Equal(t, 76, m.detail.Width)
		m = press(m, "esc")
		require.False(t, m.fullscreen)
		require.Equal(t, fromTask, m.taskOpen)
		require.Equal(t, section, m.section)
		require.Equal(t, selected, m.currentTask())
	}
}

func TestDashboardCountsAllRealTasksAndCapturesKeys(t *testing.T) {
	m := testModel()
	m.taskFilter = tasksClosed
	m.search.SetValue("nothing")
	m = press(m, "v")
	out := m.dashboardContent(120)
	require.Contains(t, out, "Tasks: 3   Finished: 1 (33%)   Open: 2 (67%)")
	require.Contains(t, out, "In progress: 0   Ready: 1   Blocked: 1")
	before := m.currentEpic()
	m = press(m, "d", "n", "D", "A", "O")
	require.Empty(t, m.pendingDelete)
	require.False(t, m.creating)
	require.False(t, m.pickerOpen)
	require.Equal(t, before, m.currentEpic())
	require.Equal(t, tabDashboard, m.tab)
	m.graph = beads.BuildGraph(map[string]beads.Issue{"loose": {ID: "loose", IssueType: "bug", Status: "closed", Priority: 2}})
	require.Contains(t, m.dashboardContent(120), "Tasks: 1   Finished: 1 (100%)")
	require.Contains(t, m.dashboardContent(120), "Closed epics: 0/0")
	m.graph = beads.BuildGraph(nil)
	require.Contains(t, m.dashboardContent(120), "No tasks yet")
	require.NotContains(t, m.dashboardContent(120), "NaN")
}

func TestRenderedNewViewsFitTerminal(t *testing.T) {
	for _, size := range [][2]int{{120, 32}, {80, 24}, {60, 20}} {
		for _, keys := range [][]string{{"v"}, {"t", "f"}, {"t", "O"}} {
			m := testModel()
			m.width, m.height = size[0], size[1]
			m = press(m, keys...)
			m.resizeDetail()
			m.renderFields()
			out := m.View()
			require.LessOrEqual(t, lipgloss.Width(out), m.width, "size %v keys %v", size, keys)
			require.LessOrEqual(t, lipgloss.Height(out), m.height, "size %v keys %v\n%s", size, keys, out)
		}
	}
}

func TestUsagePollingAndStaleness(t *testing.T) {
	m := testModel()
	require.Nil(t, m.usageCmd())
	m = press(m, "v")
	require.NotNil(t, m.usageCmd())
	require.Nil(t, m.usageCmd())
	now := time.Now()
	next, _ := m.Update(usageLoadedMsg{index: 0, snapshot: usage.Snapshot{Provider: "Codex", FetchedAt: now, Windows: []usage.Window{{Name: "5h", Used: 17, ResetsAt: now.Add(time.Hour)}}}})
	m = next.(model)
	require.False(t, m.usage[0].inFlight)
	require.Contains(t, m.usageSummary(0, true), "17% used")
	next, _ = m.Update(usageLoadedMsg{index: 0, snapshot: usage.Snapshot{Provider: "Codex", Error: "offline"}})
	m = next.(model)
	require.Contains(t, m.usageSummary(0, true), "STALE")
	require.Contains(t, m.usageSummary(0, true), "17%")
	require.NotContains(t, m.usageSummary(1, true), "0%")
}

func TestLiveCloseLeavesFilteredTaskDetail(t *testing.T) {
	m := press(testModel(), "t", "O", "f")
	issue := m.graph.Issues["a.2"]
	issue.Status = "closed"
	issues := map[string]beads.Issue{}
	for id, is := range m.graph.Issues {
		issues[id] = is
	}
	issues["a.2"] = issue
	next, _ := m.adopt(beads.BuildGraph(issues), 2)
	m = next.(model)
	require.False(t, m.taskOpen)
	require.False(t, m.fullscreen)
	require.Empty(t, m.visibleTasks())
	require.True(t, strings.Contains(m.View(), "no open tasks"))
}
