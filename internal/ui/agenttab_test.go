package ui

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pavlabs/beadsboard/internal/agentreg"
)

// registryModel is a board with no in-process agents but a registry record —
// the state after a restart, or when someone else's session registered itself.
func registryModel() model {
	m := testModel()
	m.tab = tabAgents
	m.showAll = true
	m.agentRecords = []agentreg.Record{{
		ID: "ext-1", BeadID: "a.2", Tool: agentreg.ToolClaude, Mode: agentreg.ModeCoding,
		Source: agentreg.SourceExternal, PID: os.Getpid(), Branch: "bead/a.2",
		SessionID: "sess-ext", Cwd: "/tmp/somewhere",
	}}
	m.agentAlive = map[string]bool{"ext-1": true}
	return m
}

// The registry is what survives a restart, so the tab bar has to count it —
// otherwise the tab silently disappears while agents are still running.
func TestTabBarCountsRegistryAgents(t *testing.T) {
	require.False(t, testModel().hasAgents())
	require.True(t, registryModel().hasAgents())
}

// An agent this board never started is listed, with its bead and who runs it.
func TestAgentsTabListsRegistryAgents(t *testing.T) {
	m := registryModel()

	rows := m.visibleAgents()
	require.Len(t, rows, 1)
	require.False(t, rows[0].managed())
	require.Equal(t, "a.2", rows[0].bead())
	require.Equal(t, "ext-1", rows[0].id())

	out := m.View()
	require.Contains(t, out, "a.2")
	require.Contains(t, out, "external")
}

// A live registry record sorts with the active agents; a dead one drops to the
// recent half rather than looking like it is still working.
func TestRegistryLivenessDrivesOrdering(t *testing.T) {
	m := registryModel()
	require.True(t, m.visibleAgents()[0].active())

	m.agentAlive = map[string]bool{"ext-1": false}
	require.False(t, m.visibleAgents()[0].active())
}

// The preview reports where the agent is, since its log lives in the process
// that runs it rather than here.
func TestRegistryPreviewExplainsItself(t *testing.T) {
	m := registryModel()
	out := m.agentPreviewContent(60, 12)

	require.Contains(t, out, "running")
	require.Contains(t, out, "sess-ext")
	require.Contains(t, out, "bead/a.2")
	require.Contains(t, out, "not run by this board")
}

// Kill, dismiss and resume need the in-process Manager. On someone else's agent
// they say so rather than silently doing nothing.
func TestRegistryAgentActionsExplainRatherThanNoOp(t *testing.T) {
	for _, key := range []string{"k", "x", "enter"} {
		m := registryModel()
		next, cmd := m.handleAgentsKey(keyMsg(key))
		m = next.(model)

		require.Nil(t, cmd, key)
		require.Contains(t, m.notice, "not run by this board", key)
	}
}

// The board footer advertises the tab; without it, A is undiscoverable.
func TestBoardFooterAdvertisesAgentsTab(t *testing.T) {
	require.Contains(t, testModel().footerLine(), "A agents")
}
