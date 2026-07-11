package ui

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pavlabs/beadsboard/internal/agent"
)

// a on a hovered epic switches to the Agents tab and issues a spawn command.
func TestSpawnKeyOpensAgentsTab(t *testing.T) {
	m := testModel()
	next, cmd := m.handleKey(keyMsg("a"))
	m = next.(model)
	require.Equal(t, tabAgents, m.tab)
	require.NotNil(t, cmd, "a issues a spawn command")
}

// A shows all agents in the Agents tab; with none spawned it shows the empty
// state prompt.
func TestAgentsTabEmptyState(t *testing.T) {
	m := testModel()
	next, _ := m.handleKey(keyMsg("A"))
	m = next.(model)
	require.Equal(t, tabAgents, m.tab)
	require.True(t, m.showAll)
	require.Contains(t, m.View(), "none — press a")
}

// The spawn prompt scopes the work and instructs the agent to stop-and-ask
// instead of guessing.
func TestBuildPromptStopAndAsk(t *testing.T) {
	task := buildPrompt("bd-1", "task", "Add cache", "/root", false, 0)
	require.Contains(t, task, "bd-1")
	require.Contains(t, task, "Add cache")
	require.Contains(t, task, agent.NeedsInputMarker)
	require.Contains(t, task, "pull request")
	require.Contains(t, task, "-C /root", "agent's bd targets the beads root")
	require.NotContains(t, task, "Closes", "no closing keyword without the sync plugin")

	epic := buildPrompt("ep-1", "epic", "Platform", "/root", false, 0)
	require.Contains(t, epic, "every open task")
}

// With the plugin on, the prompt asks for a PR that closes the tracking issue:
// by number when known, otherwise resolved by the agent from external_ref.
func TestBuildPromptClosesIssue(t *testing.T) {
	known := buildPrompt("bd-1", "task", "Add cache", "/root", true, 42)
	require.Contains(t, known, "Closes #42")

	unknown := buildPrompt("bd-1", "task", "Add cache", "/root", true, 0)
	require.Contains(t, unknown, "external_ref")
	require.Contains(t, unknown, "Closes #N")
}

// Settings navigation adjusts the in-memory config without saving.
func TestSettingsAdjust(t *testing.T) {
	m := testModel()
	m.openSettings()
	require.True(t, m.settingsOpen)

	m.setField = setMaxAgents
	before := m.cfg.MaxAgents
	m.adjustSetting(1)
	require.Equal(t, before+1, m.cfg.MaxAgents)

	m.setField = setPermMode
	m.cfg.PermissionMode = "acceptEdits"
	m.adjustSetting(1)
	require.Equal(t, "plan", m.cfg.PermissionMode)
	m.adjustSetting(-1)
	require.Equal(t, "acceptEdits", m.cfg.PermissionMode)

	m.setField = setMaxTurns
	m.cfg.MaxTurns = 0
	m.adjustSetting(-1)
	require.Equal(t, 0, m.cfg.MaxTurns, "turns floor at 0 (uncapped)")
}
