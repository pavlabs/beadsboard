package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/pavlabs/beadsboard/internal/beads"
)

type taskCounts struct {
	total, finished, active, ready, blocked int
}

func (c *taskCounts) add(is beads.Issue, status string) {
	c.total++
	if is.Status == "closed" {
		c.finished++
	}
	switch status {
	case beads.StatusWIP:
		c.active++
	case beads.StatusReady:
		c.ready++
	case beads.StatusBlocked:
		c.blocked++
	}
}

func countPercent(n, total int) string {
	if total == 0 {
		return fmt.Sprintf("%d (n/a)", n)
	}
	return fmt.Sprintf("%d (%.0f%%)", n, float64(n)*100/float64(total))
}

func (m model) dashboardContent(width int) string {
	if m.graph == nil {
		return "Loading dashboard…"
	}
	var total taskCounts
	var priorities [6]taskCounts // last bucket retains unexpected priorities
	epics, finishedEpics := 0, 0
	for id, is := range m.graph.Issues {
		if beads.Synthetic(id) {
			continue
		}
		if is.IsEpic() {
			epics++
			if is.Status == "closed" {
				finishedEpics++
			}
			continue
		}
		total.add(is, m.graph.TaskStatus[id])
		p := is.Priority
		if p < 0 || p > 4 {
			p = 5
		}
		priorities[p].add(is, m.graph.TaskStatus[id])
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Dashboard") + "\n\n")
	b.WriteString(m.usageSummary(0, true) + "\n" + m.usageSummary(1, true) + "\n\n")
	b.WriteString("Account windows are provider-reported; a daily limit may not exist.\n\n")
	fmt.Fprintf(&b, "Tasks: %d   Finished: %s   Open: %s\n", total.total, countPercent(total.finished, total.total), countPercent(total.total-total.finished, total.total))
	fmt.Fprintf(&b, "In progress: %d   Ready: %d   Blocked: %d\n", total.active, total.ready, total.blocked)
	fmt.Fprintf(&b, "Closed epics: %d/%d   Need attention: %d\n\n", finishedEpics, epics, m.attentionCount())
	if total.total == 0 {
		b.WriteString("No tasks yet. Create tasks to see completion metrics.\n\n")
	}
	b.WriteString(titleStyle.Render("Tasks by priority") + "\n")
	if width >= 100 {
		fmt.Fprintf(&b, "%-9s %6s %15s %15s %15s %15s %15s\n", "Priority", "Total", "Finished", "Open", "In progress", "Ready", "Blocked")
	}
	for p, c := range priorities {
		if p == 5 && c.total == 0 {
			continue
		}
		label := fmt.Sprintf("P%d", p)
		if p == 5 {
			label = "Other"
		}
		if width >= 100 {
			fmt.Fprintf(&b, "%-9s %6d %15s %15s %15s %15s %15s\n", label, c.total, countPercent(c.finished, c.total), countPercent(c.total-c.finished, c.total), countPercent(c.active, c.total), countPercent(c.ready, c.total), countPercent(c.blocked, c.total))
		} else {
			fmt.Fprintf(&b, "%s: %d tasks\n  Finished %s   Open %s\n  In progress %s   Ready %s\n  Blocked %s\n", label, c.total, countPercent(c.finished, c.total), countPercent(c.total-c.finished, c.total), countPercent(c.active, c.total), countPercent(c.ready, c.total), countPercent(c.blocked, c.total))
		}
	}
	b.WriteString("\nPercentages use each row's total tasks.\nOpen means unfinished; ready, in progress and blocked are subsets.\nReady means open with no unresolved blocking dependencies.\nEpics and display buckets are excluded from task counts.")
	return lipgloss.NewStyle().Width(max(width, 1)).Render(b.String())
}

func (m model) dashboardView(width, height int) string {
	v := viewport.New(width, height)
	v.SetContent(m.dashboardContent(width))
	v.SetYOffset(m.dashboardOffset)
	return v.View()
}

func (m model) handleDashboardKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "m":
		m.tab = m.dashboardFrom
		m.resizeDetail()
		m.renderFields()
	case "down", "j":
		m.dashboardOffset++
	case "up", "k":
		m.dashboardOffset--
	case "pgdown", " ":
		m.dashboardOffset += m.rightInnerH()
	case "pgup":
		m.dashboardOffset -= m.rightInnerH()
	case "home":
		m.dashboardOffset = 0
	case "end":
		m.dashboardOffset = len(strings.Split(m.dashboardContent(max(m.width-4, 1)), "\n"))
	}
	maxOffset := max(len(strings.Split(m.dashboardContent(max(m.width-4, 1)), "\n"))-m.rightInnerH(), 0)
	m.dashboardOffset = min(max(m.dashboardOffset, 0), maxOffset)
	return m, nil
}

func (m model) taskFilterName() string {
	switch m.taskFilter {
	case tasksOpen:
		return "open"
	case tasksClosed:
		return "closed"
	default:
		return "all"
	}
}
