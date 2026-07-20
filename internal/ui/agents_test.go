package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pavlabs/beadsboard/internal/agent"
	"github.com/pavlabs/beadsboard/internal/agentreg"
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

// beadAgents keeps only the current bead's cached registry records, orders live
// ones first, and reports liveness from the cached map.
func TestBeadAgentsExternal(t *testing.T) {
	now := time.Now()
	recs := []agentreg.Record{
		{ID: "ext-idle", BeadID: "x", Tool: agentreg.ToolCodex, Mode: agentreg.ModePlanning, Source: agentreg.SourceExternal, StartedAt: now},
		{ID: "ext-live", BeadID: "x", Tool: agentreg.ToolClaude, Mode: agentreg.ModeCoding, Source: agentreg.SourceExternal, StartedAt: now.Add(time.Second)},
		{ID: "ext-other", BeadID: "y", Tool: agentreg.ToolClaude, Mode: agentreg.ModeCoding, Source: agentreg.SourceExternal, StartedAt: now},
	}
	alive := map[string]bool{"ext-idle": false, "ext-live": true, "ext-other": true}

	tests := []struct {
		name    string
		bead    string
		wantIDs []string // expected rows, in order
	}{
		{"live first then idle, scoped to bead", "x", []string{"ext-live", "ext-idle"}},
		{"other bead is filtered out", "y", []string{"ext-other"}},
		{"unknown bead has no agents", "z", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testModel() // manager has no live agents, so rows are records-only
			m.agentRecords, m.agentAlive = recs, alive
			rows := m.beadAgents(tt.bead)
			var gotIDs []string
			for _, r := range rows {
				gotIDs = append(gotIDs, r.id)
				require.False(t, r.internal, "record-backed rows are external")
				require.Equal(t, alive[r.id], r.alive)
			}
			require.Equal(t, tt.wantIDs, gotIDs)
		})
	}
}

// A live in-process agent surfaces as an internal row with local defaults; a
// registry record sharing its ID enriches that row instead of duplicating it,
// and the live view (not the record's map entry) drives liveness.
func TestBeadAgentsInternalAndDedupe(t *testing.T) {
	repo := gitRepoUI(t)
	mgr := agent.New(repo, stubSleepClaude(t), 10)
	bead := fmt.Sprintf("bdint-%d", time.Now().UnixNano())
	view, err := mgr.Spawn(agent.Spec{IssueID: bead, Scope: "task", RepoDir: repo})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Kill(view.ID) })

	m := testModel()
	m.mgr = mgr

	rows := m.beadAgents(bead)
	require.Len(t, rows, 1)
	require.True(t, rows[0].internal)
	require.Equal(t, view.ID, rows[0].id)
	require.Equal(t, "claude", rows[0].tool)
	require.Equal(t, "coding", rows[0].mode)
	require.Equal(t, "local", rows[0].source)
	require.True(t, rows[0].alive, "a running agent is alive")

	// Same-ID record: enriches tool/mode/source, does not add a second row, and
	// its (stale) alive=false does not override the live view's liveness.
	m.agentRecords = []agentreg.Record{{
		ID: view.ID, BeadID: bead, Tool: agentreg.ToolCodex,
		Mode: agentreg.ModePlanning, Source: agentreg.SourceExternal,
	}}
	m.agentAlive = map[string]bool{view.ID: false}

	rows = m.beadAgents(bead)
	require.Len(t, rows, 1, "matching record enriches rather than duplicates")
	require.True(t, rows[0].internal)
	require.Equal(t, "codex", rows[0].tool)
	require.Equal(t, "planning", rows[0].mode)
	require.Equal(t, "external", rows[0].source)
	require.True(t, rows[0].alive, "liveness comes from the live view, not the record map")
}

// gitRepoUI makes a throwaway repo with one commit so an agent worktree can be
// added from it.
func gitRepoUI(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		require.NoError(t, cmd.Run(), "git %v", args)
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "tester")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644))
	run("add", ".")
	run("commit", "-q", "-m", "init")
	return repo
}

// stubSleepClaude writes a fake `claude` that blocks, so the spawned agent stays
// Running until the test kills it.
func stubSleepClaude(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nsleep 30\n"), 0o755))
	return path
}
