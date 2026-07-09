package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/pavlabs/beadsboard/internal/beads"
)

var (
	green  = lipgloss.Color("42")
	yellow = lipgloss.Color("214")
	cyan   = lipgloss.Color("44")
	grey   = lipgloss.Color("244")
	dim    = lipgloss.Color("240")
	mag    = lipgloss.Color("170")

	titleStyle    = lipgloss.NewStyle().Bold(true)
	dimStyle      = lipgloss.NewStyle().Foreground(dim)
	labelStyle    = lipgloss.NewStyle().Foreground(grey)
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("236"))

	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(mag)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(dim).
			Padding(0, 1)
)

// statusMeta is the display metadata for one status: its glyph, its word, and
// the colour both render in.
type statusMeta struct {
	mark  string
	word  string
	color lipgloss.Color
}

var statusTable = map[string]statusMeta{
	beads.StatusDone:    {"✓", "done", green},
	beads.StatusWIP:     {"▶", "in progress", yellow},
	beads.StatusReady:   {"●", "ready", cyan},
	beads.StatusBlocked: {"○", "blocked", dim},
	beads.StatusOpen:    {"●", "open", cyan},
}

func statusOf(status string) statusMeta {
	if s, ok := statusTable[status]; ok {
		return s
	}
	return statusTable[beads.StatusOpen]
}

// glyph returns the coloured status marker for a task or epic status.
func glyph(status string) string {
	s := statusOf(status)
	return lipgloss.NewStyle().Foreground(s.color).Render(s.mark)
}

// statusWord returns the coloured status label.
func statusWord(status string) string {
	s := statusOf(status)
	return lipgloss.NewStyle().Foreground(s.color).Render(s.word)
}

// statusMark is the uncoloured glyph, for use inside the selected-row highlight.
func statusMark(status string) string { return statusOf(status).mark }

// shortID abbreviates a bd id the way beads-plan does: "#N" for a task child,
// the trailing segment otherwise.
func shortID(id string) string {
	if i := strings.LastIndexByte(id, '.'); i >= 0 {
		return "#" + id[i+1:]
	}
	if i := strings.LastIndexByte(id, '-'); i >= 0 {
		return id[i+1:]
	}
	return id
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return string(r[:w-1]) + "…"
}
