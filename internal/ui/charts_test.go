package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/pavlabs/beadsboard/internal/beads"
	"github.com/pavlabs/beadsboard/internal/usage"
	"github.com/stretchr/testify/require"
)

func TestChartSlicesPartitionTasks(t *testing.T) {
	var c taskCounts
	c.add(beads.Issue{Status: "closed"}, beads.StatusReady)
	c.add(beads.Issue{Status: "in_progress"}, beads.StatusWIP)
	c.add(beads.Issue{Status: "open"}, beads.StatusReady)
	c.add(beads.Issue{Status: "blocked"}, beads.StatusBlocked)
	c.add(beads.Issue{Status: "deferred"}, beads.StatusOpen)
	sum := 0
	for _, s := range c.slices() {
		require.Equal(t, 1, s.count)
		sum += s.count
	}
	require.Equal(t, c.total, sum)
	for _, counts := range []taskCounts{{}, c, {total: 1, finished: 1}} {
		pie := taskPie(counts, 20)
		require.Equal(t, 20, lipgloss.Width(pie))
		require.Equal(t, 10, lipgloss.Height(pie))
		require.NotContains(t, taskOverview(counts, 60), "NaN")
	}
}

func TestQuotaChartsPreserveMissingAndStaleData(t *testing.T) {
	m := testModel()
	m.usage[0].snapshot = usage.Snapshot{Error: "signed out"}
	require.Contains(t, m.usageCharts(0, 40), "unavailable")
	require.NotContains(t, m.usageCharts(0, 40), "0%")
	m.usage[0].snapshot = usage.Snapshot{FetchedAt: time.Now(), Windows: []usage.Window{{Name: "5h", Used: 97}, {Name: "7d", Used: 32}}}
	require.Contains(t, m.usageCharts(0, 40), "97% used")
	require.Contains(t, m.usageCharts(0, 40), "32% used")
	require.Contains(t, m.usageStatusBar(0, 120), "█")
	m.usage[0].snapshot.Error = "refresh error"
	require.Contains(t, m.usageCharts(0, 40), "STALE")
	require.Contains(t, m.usageStatusBar(0, 120), "STALE")
	require.Contains(t, m.usageCharts(0, 40), "97% used")
	for _, width := range []int{36, 56, 76, 116} {
		for _, line := range strings.Split(m.dashboardContent(width), "\n") {
			require.LessOrEqual(t, lipgloss.Width(line), width)
		}
	}
}
