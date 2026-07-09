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

func (m *model) resizeDetail() {
	_, rightOuter, innerH := m.layout()
	m.detail.Width = max(rightOuter-4, 1) // border + padding
	m.detail.Height = innerH
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
	keys := "↑/↓ move · → open · ← back · e edit · r refresh · q quit"
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
	label := labelStyle.Render("edit " + shortID(m.currentID()) + ": ")
	hint := dimStyle.Render("  · tab switch · enter open · esc cancel")
	return "  " + label + strings.Join(fields, " ") + hint
}

func (m model) panes() string {
	leftOuter, _, innerH := m.layout()

	left := boxStyle.Width(leftOuter - 2).Height(innerH).Render(m.listContent(leftOuter-4, innerH))
	right := boxStyle.Width(m.detail.Width + 2).Height(innerH).Render(m.detail.View())

	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
}

// listContent renders the epic or task list (per level) with windowed scrolling.
func (m model) listContent(width, height int) string {
	items := m.currentItems()
	if len(items) == 0 {
		return dimStyle.Render("no issues")
	}

	var head string
	if m.level == focusTasks {
		epic := m.graph.Epics[m.epicCursor]
		head = titleStyle.Render(truncate(m.graph.Issues[epic].Title, width))
	} else {
		head = dimStyle.Render(fmt.Sprintf("EPICS (%d)", len(items)))
	}
	rows := max(height-2, 1) // header + spacer

	start := windowStart(len(items), m.cursor(), rows)
	end := min(start+rows, len(items))

	var b strings.Builder
	b.WriteString(head)
	b.WriteByte('\n')
	b.WriteByte('\n')
	for i := start; i < end; i++ {
		b.WriteString(m.renderRow(items[i], i == m.cursor(), width))
		if i < end-1 {
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

// syncDetail refreshes the detail pane for the currently highlighted item.
func (m *model) syncDetail() {
	if m.graph == nil {
		return
	}
	id := m.currentID()
	if id == "" {
		m.detail.SetContent("")
		return
	}
	is := m.graph.Issues[id]
	if is.IsEpic() {
		m.detail.SetContent(m.epicDetail(id))
	} else {
		m.detail.SetContent(m.taskDetail(id))
	}
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

func (m model) taskDetail(id string) string {
	is := m.graph.Issues[id]

	var b strings.Builder
	detailHeader(&b, is, id, m.graph.TaskStatus[id])
	if refs := m.blockerRefs(id); len(refs) > 0 {
		fmt.Fprintf(&b, "%s\n", labelStyle.Render("blocked by: "+strings.Join(refs, ", ")))
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
