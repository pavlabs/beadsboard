package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavlabs/beadsboard/internal/usage"
)

const usageInterval = time.Minute

type usageState struct {
	snapshot usage.Snapshot
	attempt  time.Time
	inFlight bool
}

type usageLoadedMsg struct {
	index    int
	snapshot usage.Snapshot
}

func (m model) showUsageBar() bool {
	return m.tab == tabDetails && m.focused && (m.taskOpen || m.section == secTasks)
}

func (m *model) usageCmd() tea.Cmd {
	if m.tab != tabDashboard && !m.showUsageBar() {
		return nil
	}
	var cmds []tea.Cmd
	for i, provider := range []string{"Codex", "Claude"} {
		s := &m.usage[i]
		interval := usageInterval
		if s.snapshot.Error != "" {
			interval = 5 * time.Minute
		}
		if s.inFlight || time.Since(s.attempt) < interval {
			continue
		}
		s.attempt, s.inFlight = time.Now(), true
		cmds = append(cmds, func() tea.Msg { return usageLoadedMsg{index: i, snapshot: usage.Fetch(context.Background(), provider)} })
	}
	return tea.Batch(cmds...)
}

func resetIn(reset, timeNow time.Time) string {
	if reset.IsZero() {
		return "reset unknown"
	}
	d := reset.Sub(timeNow)
	if d <= 0 {
		return "reset due"
	}
	if d >= 24*time.Hour {
		return fmt.Sprintf("resets %dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	}
	if d >= time.Hour {
		return fmt.Sprintf("resets %dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("resets %dm", max(int(d.Minutes()), 1))
}

func (m model) usageSummary(index int, detailed bool) string {
	provider := []string{"Codex", "Claude"}[index]
	s := m.usage[index].snapshot
	if len(s.Windows) == 0 {
		if s.Error != "" {
			return provider + ": unavailable (" + s.Error + ")"
		}
		return provider + ": loading account limits"
	}
	parts := []string{provider}
	now := time.Now()
	for i, w := range s.Windows {
		if !detailed && i >= 2 {
			break
		}
		parts = append(parts, fmt.Sprintf("%s %.0f%% used (%s)", w.Name, w.Used, resetIn(w.ResetsAt, now)))
	}
	if s.Error != "" || now.Sub(s.FetchedAt) > 2*usageInterval {
		parts = append(parts, "STALE")
	} else if detailed {
		parts = append(parts, "updated "+s.FetchedAt.Local().Format("15:04:05"))
	}
	sep := "  "
	if detailed {
		sep = "\n  "
	}
	out := strings.Join(parts, sep)
	if detailed && s.Error != "" {
		out += "\n  " + s.Error
	}
	return out
}
