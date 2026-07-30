package ui

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/pavlabs/beadsboard/internal/attention"
)

// attentionItems recomputes what currently wants the user, from the manager's
// live view of its own agents plus the last registry and graph snapshots. It is
// derived rather than stored so it can never disagree with what the board shows.
func (m model) attentionItems() []attention.Item {
	return attention.Collect(m.mgr.Snapshot(), m.agentRecords, m.pulls, m.graph, time.Now())
}

// openInbox shows the board-wide attention list, parking the cursor on the top
// (most severe) item.
func (m model) openInbox() (tea.Model, tea.Cmd) {
	m.inboxOpen = true
	m.inboxCursor = 0
	return m, nil
}

func (m model) handleInboxKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.attentionItems()
	switch msg.String() {
	case "esc", "i", "q":
		m.inboxOpen = false
		return m, nil
	case "down", "j":
		if m.inboxCursor < len(items)-1 {
			m.inboxCursor++
		}
		return m, nil
	case "up", "k":
		if m.inboxCursor > 0 {
			m.inboxCursor--
		}
		return m, nil
	case "enter":
		if m.inboxCursor < len(items) {
			return m.jumpToBead(items[m.inboxCursor].Bead)
		}
		return m, nil
	case "o":
		if m.inboxCursor < len(items) {
			return m, openURL(items[m.inboxCursor].URL)
		}
		return m, nil
	}
	return m, nil
}

// openURL hands a pull request to the browser. A board-side failure to launch
// one is not worth interrupting the user over, so the result is dropped.
func openURL(url string) tea.Cmd {
	if url == "" {
		return nil
	}
	return func() tea.Msg {
		_ = exec.Command("open", url).Run()
		return nil
	}
}

// jumpToBead moves the board's cursor onto a bead and closes the inbox, so
// acting on an attention item lands where the work is. A task drills into its
// epic's list; an epic just selects it.
func (m model) jumpToBead(bead string) (tea.Model, tea.Cmd) {
	m.inboxOpen = false
	if m.graph == nil || bead == "" {
		return m, nil
	}
	epic := bead
	if !m.graph.Issues[bead].IsEpic() {
		epic = m.graph.EpicOf(bead)
	}
	for i, id := range m.graph.Epics {
		if id != epic {
			continue
		}
		m.epicCursor = i
		m.taskCursor = 0
		m.taskOpen = false
		m.focused = false
		m.tab = tabDetails
		if epic != bead {
			for j, task := range m.graph.Tasks[epic] {
				if task == bead {
					m.taskCursor = j
					m.focused = true
					m.section = secTasks
					break
				}
			}
		}
		m.clampCursors()
		m.syncDetail()
		return m, m.commentsCmd()
	}
	return m, nil
}

// inboxView lists every attention item board-wide: what wants you, which bead,
// and why. Items are pre-sorted most-urgent-first by the attention package.
func (m model) inboxView(width, height int) string {
	items := m.attentionItems()

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", dimStyle.Render("ATTENTION  board-wide"))
	if len(items) == 0 {
		b.WriteString(dimStyle.Render("  nothing needs you"))
		return b.String()
	}

	rows := max(height-3, 1)
	start := windowStart(len(items), m.inboxCursor, rows)
	for i := start; i < len(items) && i < start+rows; i++ {
		it := items[i]
		reason, subject := fmt.Sprintf("%-12s", it.Reason), fmt.Sprintf("%-16s", it.Subject())
		detailW := max(width-lipgloss.Width(reason)-lipgloss.Width(subject)-3, 4)
		detail := truncate(firstLine(it.Detail), detailW)
		if i == m.inboxCursor {
			// Re-render plainly so the highlight background reads cleanly.
			plain := fmt.Sprintf("%s %s %s", reason, subject, detail)
			b.WriteString(selectedStyle.Width(width).Render(truncate(plain, width)))
		} else {
			colored := lipgloss.NewStyle().Foreground(reasonColor(it.Reason)).Render(reason)
			b.WriteString("  " + colored + " " + subject + " " + dimStyle.Render(detail))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// attentionCount is what the header badge reports.
func (m model) attentionCount() int { return len(m.attentionItems()) }

func reasonColor(r attention.Reason) lipgloss.Color {
	switch r {
	case attention.NeedsInput:
		return yellow
	case attention.Failed:
		return lipgloss.Color("203")
	case attention.ChangesRequested, attention.ChecksFailing, attention.Conflicted:
		return lipgloss.Color("203")
	case attention.ReviewRequired:
		return cyan
	case attention.ReadyToMerge:
		return green
	case attention.Stalled:
		return grey
	}
	return dim
}
