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
		SessionID: "sess-ext", Cwd: "/tmp/somewhere", PaneID: "terminal_7",
	}}
	m.agentAlive = map[string]bool{"ext-1": true}
	return m
}

// Navigation stays discoverable with or without registered agents.
func TestTabBarAlwaysShowsAgentsAndDashboard(t *testing.T) {
	for _, m := range []model{testModel(), registryModel()} {
		require.Contains(t, m.panes(), "Agents")
		require.Contains(t, m.panes(), "Dashboard (v)")
	}
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

// Dismiss needs the in-process Manager. On someone else's agent it says so
// rather than silently doing nothing.
func TestRegistryAgentActionsExplainRatherThanNoOp(t *testing.T) {
	for _, key := range []string{"x"} {
		m := registryModel()
		next, cmd := m.handleAgentsKey(keyMsg(key))
		m = next.(model)

		require.Nil(t, cmd, key)
		require.Contains(t, m.notice, "not run by this board", key)
	}
}

// k on a registry-only row uses the shared registry control path and removes
// the record, matching the task-detail ledger's external kill behavior.
func TestRegistryAgentKill(t *testing.T) {
	m := registryModel()
	dir := t.TempDir()
	m.reg = agentreg.New(dir)
	rec := *m.visibleAgents()[0].rec
	rec.PID = 0 // no process signal in this focused routing test
	require.NoError(t, m.reg.Put(rec))

	_, cmd := m.handleAgentsKey(keyMsg("k"))
	require.NotNil(t, cmd, "registry refresh follows the kill")
	got, err := m.reg.List()
	require.NoError(t, err)
	require.Empty(t, got)
}

// Enter on a live external session reattaches its existing pane. It must not
// start a second process by resuming the same session concurrently.
func TestRegistryAgentEnterReattachesLivePane(t *testing.T) {
	t.Setenv("ZELLIJ", "")
	m := registryModel()
	next, cmd := m.handleAgentsKey(keyMsg("enter"))
	m = next.(model)
	require.NotNil(t, cmd)
	require.Empty(t, m.notice)

	msg := cmd().(interveneMsg)
	require.ErrorContains(t, msg.err, "reattach pane terminal_7")
	require.NotContains(t, msg.err.Error(), "--resume")
}

// An ended registry session is no longer attachable, so Enter falls back to
// the backend's resumable-session command using the recorded cwd/session id.
func TestRegistryAgentEnterResumesInactiveSession(t *testing.T) {
	t.Setenv("ZELLIJ", "")
	m := registryModel()
	m.agentAlive["ext-1"] = false
	next, cmd := m.handleAgentsKey(keyMsg("enter"))
	_ = next.(model)
	require.NotNil(t, cmd)

	msg := cmd().(interveneMsg)
	require.ErrorContains(t, msg.err, "cd /tmp/somewhere")
	require.ErrorContains(t, msg.err, "sess-ext")
}

// The board footer advertises the tab; without it, A is undiscoverable.
func TestBoardFooterAdvertisesAgentsTab(t *testing.T) {
	require.Contains(t, testModel().footerLine(), "A agents")
}

// Once in the Agents tab, every control affordance is discoverable in-place.
func TestAgentsFooterAdvertisesControls(t *testing.T) {
	m := registryModel()
	f := m.footerLine()
	require.Contains(t, f, "↑/↓ select")
	require.Contains(t, f, "enter intervene/reattach")
	require.Contains(t, f, "k kill")
	require.Contains(t, f, "A all")
}
