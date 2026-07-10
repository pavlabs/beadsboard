package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/pavlabs/beadsboard/internal/beads"
)

// layout returns the outer widths of the two panes and their shared inner
// height, all derived from the current terminal size.
func (m model) layout() (leftOuter, rightOuter, innerH int) {
	leftOuter = min(46, max(m.width/2, 1))
	rightOuter = max(m.width-leftOuter-1, 10)
	innerH = max(m.height-2 /*header+footer*/ -2 /*pane borders*/, 1)
	return
}

// rightSplit divides the right pane's inner height evenly between the epic
// metadata (top) and the task list (bottom). The two stacked boxes add 4 border
// rows total versus the single left box's 2, so the usable content is innerH-2.
func rightSplit(innerH int) (topContent, botContent int) {
	usable := max(innerH-2, 2)
	topContent = usable / 2
	botContent = usable - topContent
	return
}

func (m *model) resizeDetail() {
	_, rightOuter, innerH := m.layout()
	m.detail.Width = max(rightOuter-4, 1) // border + padding
	if m.taskOpen {
		m.detail.Height = innerH // task detail owns the whole right pane
	} else {
		topContent, _ := rightSplit(innerH)
		m.detail.Height = topContent
	}
	m.input.Width = max(m.detail.Width-14, 8)
	m.area.SetWidth(max(m.detail.Width, 8))
	m.area.SetHeight(max(m.detail.Height-4, 3))
}

func (m model) View() string {
	if m.width == 0 {
		return ""
	}
	header := m.headerLine()
	footer := m.footerLine()

	var body string
	switch {
	case m.graph == nil && m.err != nil:
		body = lipgloss.NewStyle().Foreground(yellow).Render("  " + m.err.Error())
	case m.graph == nil:
		body = fmt.Sprintf("  %s hydrating issues…", m.spinner.View())
	default:
		body = m.panes()
	}

	return strings.Join([]string{header, body, footer}, "\n")
}

func (m model) headerLine() string {
	title := headerStyle.Render("beadsboard")
	sub := dimStyle.Render("  " + m.client.Dir)
	if m.loading && m.graph != nil {
		sub += "  " + m.spinner.View() + dimStyle.Render(" refreshing")
	} else if m.err != nil && m.graph != nil {
		sub += lipgloss.NewStyle().Foreground(yellow).Render("  ⚠ " + m.err.Error())
	}
	return title + sub
}

func (m model) footerLine() string {
	var keys string
	switch {
	case m.editing:
		switch m.editSec {
		case secStatus, secPriority:
			keys = "←/→ change · enter save · esc cancel"
		case secTitle:
			keys = "enter save · esc cancel"
		default:
			keys = "ctrl+s save · esc cancel · enter = newline"
		}
	case m.taskOpen:
		keys = "tab field · e edit · ↑/↓ scroll · esc back · q quit"
	case m.focused && m.section == secTasks:
		keys = "↑/↓ task · enter open · tab section · esc back · q quit"
	case m.focused:
		keys = "tab section · e edit · ↑/↓ scroll · esc back · q quit"
	default:
		keys = "↑/↓ move · → open · r refresh · q quit"
	}
	return dimStyle.Render("  " + keys)
}

func (m model) panes() string {
	leftOuter, rightOuter, innerH := m.layout()
	rightInner := max(rightOuter-4, 1)
	left := boxStyle.Width(leftOuter - 2).Height(innerH).Render(m.epicsContent(leftOuter-4, innerH))

	var right string
	if m.taskOpen {
		// A task's detail page takes the whole right pane — it has no subtasks.
		right = boxStyle.Width(rightOuter - 2).Height(innerH).Render(m.detail.View())
	} else {
		topContent, botContent := rightSplit(innerH)
		fields := boxStyle.Width(rightOuter - 2).Height(topContent).Render(m.detail.View())
		tasks := boxStyle.Width(rightOuter - 2).Height(botContent).Render(m.taskBox(rightInner, botContent))
		right = lipgloss.JoinVertical(lipgloss.Left, fields, tasks)
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
}

// taskBox renders the lower-right region: just the task list normally, or the
// list split beside a read-only preview of the hovered task once the task-list
// section is focused.
func (m model) taskBox(width, height int) string {
	if !m.focused || m.section != secTasks {
		return m.taskListContent(width, height)
	}
	listW := max(width/2, 8)
	prevW := max(width-listW-1, 8)
	list := m.taskListContent(listW, height)
	preview := m.taskPreviewContent(prevW, height)
	return lipgloss.JoinHorizontal(lipgloss.Top, list, " ", preview)
}

// taskPreviewContent renders a read-only field summary of the hovered task,
// clipped to the region height.
func (m model) taskPreviewContent(width, height int) string {
	id := m.currentTask()
	if id == "" {
		return dimStyle.Render("no task")
	}
	lines := strings.Split(m.fields(id, width), "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// epicsContent renders the epic list (left pane) with windowed scrolling.
func (m model) epicsContent(width, height int) string {
	epics := m.graph.Epics
	if len(epics) == 0 {
		return dimStyle.Render("no epics")
	}
	rows := max(height-2, 1) // header + spacer

	start := windowStart(len(epics), m.epicCursor, rows)
	end := min(start+rows, len(epics))

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", dimStyle.Render(fmt.Sprintf("EPICS (%d)", len(epics))))
	for i := start; i < end; i++ {
		b.WriteString(m.renderRow(epics[i], i == m.epicCursor, width))
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// taskListContent renders the current epic's tasks in the lower-right region,
// highlighting the task cursor when the task-list section is focused.
func (m model) taskListContent(width, height int) string {
	tasks := m.currentEpicTasks()
	active := m.focused && m.section == secTasks

	var b strings.Builder
	hdr := fmt.Sprintf("TASKS (%d)", len(tasks))
	if active {
		b.WriteString(selectedStyle.Render(" " + hdr + " "))
	} else {
		b.WriteString(dimStyle.Render(hdr))
	}
	if len(tasks) == 0 {
		fmt.Fprintf(&b, "\n%s", dimStyle.Render("no tasks"))
		return b.String()
	}
	b.WriteString("\n\n")
	rows := max(height-2, 1)

	start := 0
	if active {
		start = windowStart(len(tasks), m.taskCursor, rows)
	}
	end := min(start+rows, len(tasks))
	for i := start; i < end; i++ {
		b.WriteString(m.renderRow(tasks[i], active && i == m.taskCursor, width))
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	if end < len(tasks) {
		fmt.Fprintf(&b, "\n%s", dimStyle.Render(fmt.Sprintf("… +%d below", len(tasks)-end)))
	}
	return b.String()
}

func (m model) renderRow(id string, selected bool, width int) string {
	is := m.graph.Issues[id]
	var status, annotation string
	if is.IsEpic() {
		status = m.graph.EpicStatus[id]
		if needs := m.graph.Prereqs[id]; len(needs) > 0 {
			annotation = "needs " + joinShort(needs, 3)
		}
	} else {
		status = m.graph.TaskStatus[id]
		switch status {
		case beads.StatusReady:
			annotation = "◀ start"
		case beads.StatusBlocked:
			annotation = "waits " + joinLimit(m.blockerRefs(id), 3)
		}
	}

	sid := shortID(id)
	// Reserve room for glyph(1)+space + id(6) + spaces + priority(3) + annotation.
	prefix := fmt.Sprintf("%s %-6s ", glyph(status), sid)
	prio := fmt.Sprintf("P%d", is.Priority)

	fixed := lipgloss.Width(prefix) + len(prio) + 2
	annoW := 0
	if annotation != "" {
		annoW = lipgloss.Width(annotation) + 2
	}
	titleW := max(width-fixed-annoW, 4)
	title := truncate(is.Title, titleW)

	line := prefix + fmt.Sprintf("%-*s ", titleW, title) + dimStyle.Render(prio)
	if annotation != "" {
		line += "  " + dimStyle.Render(annotation)
	}

	if selected {
		// Re-render plainly so the highlight background reads cleanly.
		plain := fmt.Sprintf("%s %-6s %-*s P%d", statusMark(status), sid, titleW, title, is.Priority)
		if annotation != "" {
			plain += "  " + annotation
		}
		return selectedStyle.Width(width).Render(truncate(plain, width))
	}
	return line
}

// syncDetail refreshes the fields region for the highlighted epic and resets
// its scroll — used when the epic changes or the window resizes.
func (m *model) syncDetail() {
	m.renderFields()
	m.detail.GotoTop()
}

// renderFields rebuilds the fields-region content, highlighting the focused
// section. Title stays pinned at the top — moving between sections only moves
// the highlight, never scrolls the metadata out of view.
func (m *model) renderFields() {
	id := m.target()
	if m.graph == nil || id == "" {
		m.detail.SetContent("")
		return
	}
	m.detail.SetContent(m.fields(id, m.detail.Width))
}

// fields lays out an issue's navigable fields (title/status/priority, then
// read-only context, then the description and notes blocks), highlighting the
// focused section. It serves both an epic and a drilled-into task; the context
// rows differ by type.
func (m model) fields(id string, width int) string {
	is := m.graph.Issues[id]
	width = max(width, 1)
	st := m.graph.EpicStatus[id]
	if !is.IsEpic() {
		st = m.graph.TaskStatus[id]
	}

	var b strings.Builder
	put := func(s string) {
		b.WriteString(s)
		b.WriteByte('\n')
	}
	ctx := func(label, val string) {
		put(labelStyle.Render(fmt.Sprintf("%-11s ", label)) + val)
	}
	short := func(sec int, label, plain, styled string) {
		switch {
		case m.editing && m.editSec == sec:
			put(labelStyle.Render(fmt.Sprintf("%-11s ", label)) + m.editShortView(sec))
		case m.focused && m.section == sec:
			put(selectedStyle.Render(truncate(fmt.Sprintf("%-11s %s", label, plain), width)))
		default:
			put(labelStyle.Render(fmt.Sprintf("%-11s ", label)) + styled)
		}
	}
	block := func(sec int, label, body string) {
		put("")
		editing := m.editing && m.editSec == sec
		if editing || (m.focused && m.section == sec) {
			put(selectedStyle.Render(" " + label + " "))
		} else {
			put(labelStyle.Render(label))
		}
		if editing {
			for _, l := range strings.Split(m.area.View(), "\n") {
				put(l)
			}
			return
		}
		if strings.TrimSpace(body) == "" {
			put(dimStyle.Render("  (none)"))
			return
		}
		for _, l := range strings.Split(lipgloss.NewStyle().Width(max(width-2, 1)).Render(body), "\n") {
			put("  " + l)
		}
	}

	short(secTitle, "title", is.Title, titleStyle.Render(is.Title))
	short(secStatus, "status", statusOf(st).word, glyph(st)+" "+statusWord(st))
	short(secPriority, "priority", fmt.Sprintf("P%d", is.Priority), fmt.Sprintf("P%d", is.Priority))

	if is.IsEpic() {
		done, total := m.graph.EpicProgress(id)
		ctx("progress", fmt.Sprintf("%d/%d done", done, total))
		if needs := m.graph.Prereqs[id]; len(needs) > 0 {
			ctx("needs", joinShort(needs, 99))
		}
		if unl := m.graph.Unlocks[id]; len(unl) > 0 {
			ctx("unlocks", joinShort(unl, 99))
		}
	} else if refs := m.blockerRefs(id); len(refs) > 0 {
		ctx("needs", joinLimit(refs, 99))
	}
	if len(is.Labels) > 0 {
		ctx("labels", strings.Join(is.Labels, ", "))
	}

	block(secDescription, "description", is.Description)
	block(secNotes, "notes", is.Notes)

	return b.String()
}

// editShortView renders the active editor for a short field: the title text box
// or the status/priority cycle chooser.
func (m model) editShortView(sec int) string {
	switch sec {
	case secTitle:
		return m.input.View()
	case secStatus:
		return selectedStyle.Render(" ‹ " + editStatuses[m.choice] + " › ")
	case secPriority:
		return selectedStyle.Render(fmt.Sprintf(" ‹ P%d › ", m.choice))
	}
	return ""
}

// blockerRefs renders a task's open blockers, qualifying any that live in a
// different epic (as "<epic>#N") so a bare "#N" is never ambiguous across epics.
func (m model) blockerRefs(task string) []string {
	fromEpic := m.graph.EpicOf(task)
	open := m.graph.OpenBlockerRefs(task)
	out := make([]string, len(open))
	for i, b := range open {
		if be := m.graph.EpicOf(b); be != "" && be != fromEpic {
			out[i] = shortID(be) + shortID(b)
		} else {
			out[i] = shortID(b)
		}
	}
	return out
}

// joinShort abbreviates each id then joins up to limit of them.
func joinShort(ids []string, limit int) string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = shortID(id)
	}
	return joinLimit(out, limit)
}

// joinLimit joins up to limit already-formatted refs with commas.
func joinLimit(refs []string, limit int) string {
	if len(refs) > limit {
		refs = refs[:limit]
	}
	return strings.Join(refs, ",")
}

// windowStart keeps the cursor visible within a scroll window of h rows.
func windowStart(n, cursor, h int) int {
	if n <= h {
		return 0
	}
	return min(max(cursor-h/2, 0), n-h)
}
