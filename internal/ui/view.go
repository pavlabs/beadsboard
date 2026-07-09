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

// rightSplit divides the right pane's inner height into the fields region (top,
// ~30%) and the task-list region (bottom). The two stacked boxes add 4 border
// rows total versus the single left box's 2, so the usable content is innerH-2.
func rightSplit(innerH int) (topContent, botContent int) {
	usable := max(innerH-2, 2)
	topContent = min(max(usable*3/10, 3), usable-1)
	botContent = usable - topContent
	return
}

func (m *model) resizeDetail() {
	_, rightOuter, innerH := m.layout()
	topContent, _ := rightSplit(innerH)
	m.detail.Width = max(rightOuter-4, 1) // border + padding
	m.detail.Height = topContent
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
	if m.editing {
		return m.editPrompt()
	}
	keys := "↑/↓ move · e edit · r refresh · q quit"
	return dimStyle.Render("  " + keys)
}

// editPrompt renders the modal field picker: each editable field, the current
// one highlighted, plus the controls.
func (m model) editPrompt() string {
	fields := make([]string, len(editFields))
	for i, f := range editFields {
		if i == m.editField {
			fields[i] = selectedStyle.Render(" " + f + " ")
		} else {
			fields[i] = dimStyle.Render(f)
		}
	}
	label := labelStyle.Render("edit " + shortID(m.currentEpic()) + ": ")
	hint := dimStyle.Render("  · tab switch · enter open · esc cancel")
	return "  " + label + strings.Join(fields, " ") + hint
}

func (m model) panes() string {
	leftOuter, rightOuter, innerH := m.layout()
	topContent, botContent := rightSplit(innerH)
	rightInner := max(rightOuter-4, 1)

	left := boxStyle.Width(leftOuter - 2).Height(innerH).Render(m.epicsContent(leftOuter-4, innerH))
	fields := boxStyle.Width(rightOuter - 2).Height(topContent).Render(m.detail.View())
	tasks := boxStyle.Width(rightOuter - 2).Height(botContent).Render(m.taskListContent(rightInner, botContent))
	right := lipgloss.JoinVertical(lipgloss.Left, fields, tasks)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
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

// taskListContent renders the current epic's tasks in the lower-right region.
func (m model) taskListContent(width, height int) string {
	tasks := m.currentEpicTasks()
	var b strings.Builder
	b.WriteString(dimStyle.Render(fmt.Sprintf("TASKS (%d)", len(tasks))))
	if len(tasks) == 0 {
		fmt.Fprintf(&b, "\n%s", dimStyle.Render("no tasks"))
		return b.String()
	}
	b.WriteString("\n\n")
	rows := max(height-2, 1)
	for i, id := range tasks {
		if i >= rows {
			fmt.Fprintf(&b, "%s", dimStyle.Render(fmt.Sprintf("… +%d more", len(tasks)-i)))
			break
		}
		b.WriteString(m.renderRow(id, false, width))
		if i < len(tasks)-1 && i < rows-1 {
			b.WriteByte('\n')
		}
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

// syncDetail refreshes the fields region with the highlighted epic's detail.
func (m *model) syncDetail() {
	if m.graph == nil {
		return
	}
	id := m.currentEpic()
	if id == "" {
		m.detail.SetContent("")
		return
	}
	// The viewport clips rather than wraps, so wrap to its width first. lipgloss
	// is ANSI-aware and won't miscount the escape bytes in the styled segments.
	m.detail.SetContent(lipgloss.NewStyle().Width(m.detail.Width).Render(m.epicDetail(id)))
	m.detail.GotoTop()
}

// detailHeader renders the title, id line, and status line shared by both
// epic and task detail views.
func detailHeader(b *strings.Builder, is beads.Issue, id, status string) {
	fmt.Fprintf(b, "%s\n", titleStyle.Render(is.Title))
	fmt.Fprintf(b, "%s\n\n", dimStyle.Render(fmt.Sprintf("%s · %s", shortID(id), id)))
	fmt.Fprintf(b, "%s  %s  P%d\n", glyph(status), statusWord(status), is.Priority)
}

func (m model) epicDetail(id string) string {
	is := m.graph.Issues[id]
	done, total := m.graph.EpicProgress(id)

	var b strings.Builder
	detailHeader(&b, is, id, m.graph.EpicStatus[id])
	fmt.Fprintf(&b, "%s\n", labelStyle.Render(fmt.Sprintf("progress: %d/%d tasks done", done, total)))
	if needs := m.graph.Prereqs[id]; len(needs) > 0 {
		fmt.Fprintf(&b, "%s\n", labelStyle.Render("needs: "+joinShort(needs, 99)))
	}
	if unl := m.graph.Unlocks[id]; len(unl) > 0 {
		fmt.Fprintf(&b, "%s\n", labelStyle.Render("unlocks: "+joinShort(unl, 99)))
	}
	writeLabels(&b, is.Labels)
	writeDescription(&b, is.Description)
	return b.String()
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

func writeLabels(b *strings.Builder, labels []string) {
	if len(labels) > 0 {
		fmt.Fprintf(b, "%s\n", labelStyle.Render("labels: "+strings.Join(labels, ", ")))
	}
}

func writeDescription(b *strings.Builder, desc string) {
	fmt.Fprintf(b, "%s\n", dimStyle.Render(strings.Repeat("─", 8)))
	if strings.TrimSpace(desc) == "" {
		b.WriteString(dimStyle.Render("(no description)"))
		return
	}
	b.WriteString(desc)
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
