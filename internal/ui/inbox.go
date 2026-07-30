package ui

import (
	"fmt"
	"net/url"
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
	return attention.Collect(m.mgr.Snapshot(), m.agentRecords, m.agentAlive, m.pulls, m.graph, time.Now())
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
	// The list shrinks under the cursor whenever an item resolves itself — an
	// agent finishes, a PR merges — so clamp before acting on the selection.
	m.inboxCursor = min(m.inboxCursor, max(len(items)-1, 0))
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
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

// openURL hands a pull request to the browser. The URL comes from GitHub's API
// rather than from a PR author, but it is checked anyway before being handed to a
// launcher: `open` getopt-parses a leading dash, and a non-https scheme would be
// dispatched to whatever handler claims it.
func openURL(raw string) tea.Cmd {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil
	}
	return func() tea.Msg {
		_ = exec.Command(browserOpener, raw).Run()
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
	// The cursors index the *visible* lists, not the graph's, so a jump taken
	// while a search filter is active has to resolve against the same view every
	// other cursor writer uses — otherwise it selects the wrong epic.
	i := indexOf(m.visibleEpics(), epic)
	if i < 0 {
		return m, nil
	}
	m.epicCursor = i
	m.taskCursor = 0
	m.taskOpen = false
	m.focused = false
	m.tab = tabDetails
	if epic != bead {
		if j := indexOf(m.visibleTasks(), bead); j >= 0 {
			m.taskCursor = j
			m.focused = true
			m.section = secTasks
		}
	}
	m.clampCursors()
	m.syncDetail()
	return m, m.commentsCmd()
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
		// Both rows carry the same two-space lead so the columns don't shift as
		// the cursor moves; the highlight just paints over it.
		reason, subject := fmt.Sprintf("%-12s", it.Reason), fmt.Sprintf("%-16s", it.Subject())
		detailW := max(width-lipgloss.Width(reason)-lipgloss.Width(subject)-4, 4)
		detail := truncate(firstLine(it.Detail), detailW)
		if i == m.inboxCursor {
			// Re-render plainly so the highlight background reads cleanly.
			plain := fmt.Sprintf("  %s %s %s", reason, subject, detail)
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
	case attention.Failed, attention.ChangesRequested, attention.ChecksFailing, attention.Conflicted:
		return red
	case attention.ReviewRequired:
		return cyan
	case attention.ReadyToMerge:
		return green
	case attention.Stalled:
		return grey
	}
	return dim
}
