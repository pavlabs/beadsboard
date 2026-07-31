package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Unwrapped, a long line is cut to the pane; wrapped, it keeps its content
// across several rows.
func TestTailLinesWrapping(t *testing.T) {
	long := "→ Bash: go test ./internal/... -run TestSomethingWithAVeryLongName -count=1 -race"

	clipped := tailLines([]string{long}, 20, 6, false)
	require.Len(t, clipped, 1)
	require.LessOrEqual(t, len([]rune(clipped[0])), 20)
	require.NotContains(t, clipped[0], "-race")

	wrapped := tailLines([]string{long}, 20, 6, true)
	require.Greater(t, len(wrapped), 1)
	require.Contains(t, strings.Join(wrapped, ""), "-race")
}

// The window is filled from the newest line backwards, so the most recent output
// is what survives a pane too small for all of it.
func TestTailLinesKeepsNewest(t *testing.T) {
	lines := []string{"oldest", "middle", "newest"}

	require.Equal(t, []string{"middle", "newest"}, tailLines(lines, 40, 2, false))
	require.Equal(t, []string{"middle", "newest"}, tailLines(lines, 40, 2, true))
}

// Wrapping never overflows the rows it was given, even when one line alone is
// taller than the pane — and what it keeps is the tail of that line.
func TestTailLinesRespectsHeight(t *testing.T) {
	long := strings.Repeat("abcde ", 40)

	for _, rows := range []int{1, 2, 3, 7} {
		got := tailLines([]string{"first", long}, 12, rows, true)
		require.LessOrEqual(t, len(got), rows, "rows=%d", rows)
		require.NotEmpty(t, got, "rows=%d", rows)
	}
}

// The toggle is board-wide, so moving through the agent list leaves it alone.
func TestWrapLogsSurvivesNavigation(t *testing.T) {
	m := testModel()
	m.tab = tabAgents

	next, _ := m.handleAgentsKey(keyMsg("w"))
	m = next.(model)
	require.True(t, m.wrapLogs)

	for _, k := range []string{"down", "up", "A"} {
		n, _ := m.handleAgentsKey(keyMsg(k))
		m = n.(model)
		require.True(t, m.wrapLogs, "%s reset the wrap toggle", k)
	}

	next, _ = m.handleAgentsKey(keyMsg("w"))
	require.False(t, next.(model).wrapLogs)
}

// The Agents tab advertises the toggle; the board's own w still means epic-title
// wrapping and must not be confused with it.
func TestAgentsFooterShowsWrap(t *testing.T) {
	m := testModel()
	m.tab = tabAgents
	require.Contains(t, m.footerLine(), "w wrap")
}
