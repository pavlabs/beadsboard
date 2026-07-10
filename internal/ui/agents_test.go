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
	task := buildPrompt("bd-1", "task", "Add cache")
	require.Contains(t, task, "bd-1")
	require.Contains(t, task, "Add cache")
	require.Contains(t, task, agent.NeedsInputMarker)
	require.Contains(t, task, "pull request")

	epic := buildPrompt("ep-1", "epic", "Platform")
	require.Contains(t, epic, "every open task")
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
