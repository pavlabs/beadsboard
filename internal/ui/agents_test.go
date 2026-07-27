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
	"github.com/pavlabs/beadsboard/internal/beads"
)

// k on the focused ledger row for a beadsboard-owned agent routes through the
// Manager's dismissal path, removing it from the manager.
func TestKillBeadAgentInternalDismisses(t *testing.T) {
	repo := gitRepoUI(t)
	mgr := agent.New(repo, stubSleepClaude(t), 10)
	view, err := mgr.Spawn(agent.Spec{IssueID: "a", Scope: "epic", RepoDir: repo})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Kill(view.ID) })

	m := testModel()
	m.mgr = mgr
	for m.epicCursor < 5 && m.currentEpic() != "a" {
		m.epicCursor++
	}
	require.Equal(t, "a", m.target(), "target is the epic the agent runs on")
	m.beadAgentCursor = 0

	m.killBeadAgent()
	require.Empty(t, m.mgr.Snapshot(), "dismiss drops the owned agent")
}

// k on the focused ledger row for an external record routes through the registry:
// it signals the PID and drops the record.
func TestKillBeadAgentExternalSignalsAndDrops(t *testing.T) {
	proc := exec.Command("sleep", "30")
	require.NoError(t, proc.Start())
	t.Cleanup(func() { _ = proc.Process.Kill() })

	reg := agentreg.New(t.TempDir())
	rec := agentreg.Record{ID: "ext-1", BeadID: "a", Tool: agentreg.ToolClaude, Mode: agentreg.ModePlanning, Source: agentreg.SourceExternal, PID: proc.Process.Pid, StartedAt: time.Now()}
	require.NoError(t, reg.Put(rec))

	m := testModel()
	m.reg = reg
	m.agentRecords = []agentreg.Record{rec}
	m.agentAlive = map[string]bool{"ext-1": true}
	for m.epicCursor < 5 && m.currentEpic() != "a" {
		m.epicCursor++
	}
	require.Equal(t, "a", m.target())
	m.beadAgentCursor = 0

	m.killBeadAgent()

	got, err := reg.List()
	require.NoError(t, err)
	require.Empty(t, got, "registry Kill drops the record after signalling")
	require.Error(t, proc.Wait(), "the signalled process exits non-zero")
}

// The Details view folds in the selected bead's cached activity timeline.
func TestDetailsRendersTimeline(t *testing.T) {
	m := testModel()
	for m.epicCursor < 5 && m.currentEpic() != "a" {
		m.epicCursor++
	}
	m.commentBead = "a"
	m.comments = []beads.Comment{
		{Author: "art", Text: "kicked off", CreatedAt: "2026-07-22T18:54:54Z"},
		{Author: "bot", Text: "bb-agent spawn agent=a-1", CreatedAt: "2026-07-22T18:55:00Z"},
	}
	out := m.fields("a", 80)
	require.Contains(t, out, "ACTIVITY")
	require.Contains(t, out, "kicked off")
	require.Contains(t, out, "bb-agent spawn agent=a-1")

	// A timeline cached for a different bead does not leak into this one.
	m.commentBead = "b"
	require.NotContains(t, m.fields("a", 80), "ACTIVITY")
}

// shortTime compacts an RFC3339 stamp and passes through anything it can't parse.
func TestShortTime(t *testing.T) {
	require.Equal(t, "unparseable", shortTime("unparseable"))
	require.NotContains(t, shortTime("2026-07-22T18:54:54Z"), "T", "parsed stamps drop the RFC3339 T")
}

// a on a hovered epic arms the launcher matrix for that bead rather than
// spawning directly; it does not switch tabs until a cell is dispatched.
func TestSpawnKeyOpensPicker(t *testing.T) {
	m := testModel()
	next, cmd := m.handleKey(keyMsg("a"))
	m = next.(model)
	require.True(t, m.pickerOpen)
	require.Equal(t, m.currentEpic(), m.pickerTarget)
	require.Equal(t, "epic", m.pickerScope)
	require.Equal(t, pickCoding, m.pickerMode, "defaults to coding")
	require.Equal(t, pickClaude, m.pickerBackend, "defaults to claude")
	require.Equal(t, tabDetails, m.tab, "arming does not switch tabs")
	require.Nil(t, cmd)
}

// The mode letters c/p move the picker row without dispatching; esc closes it.
func TestPickerModeSelectAndClose(t *testing.T) {
	m := testModel()
	m.openPicker("a", "epic")

	next, cmd := m.handleKey(keyMsg("p"))
	m = next.(model)
	require.True(t, m.pickerOpen, "a mode letter only moves the row")
	require.Equal(t, pickPlanning, m.pickerMode)
	require.Nil(t, cmd)

	next, _ = m.handleKey(keyMsg("c"))
	m = next.(model)
	require.Equal(t, pickCoding, m.pickerMode)

	next, _ = m.handleKey(keyMsg("esc"))
	m = next.(model)
	require.False(t, m.pickerOpen, "esc closes the picker")
}

// The chord a-c-l lands the coding+claude cell: it dispatches a spawn and
// switches to the Agents tab.
func TestPickerChordCodingClaude(t *testing.T) {
	m := testModel()
	m.openPicker("a", "epic")
	next, _ := m.handleKey(keyMsg("c"))
	m = next.(model)
	next, cmd := m.handleKey(keyMsg("l"))
	m = next.(model)
	require.False(t, m.pickerOpen, "the tool letter dispatches and closes")
	require.Equal(t, tabAgents, m.tab, "coding switches to the Agents tab")
	require.NotNil(t, cmd, "dispatch issues a spawn command")
}

// The chord a-p-l lands the planning+claude cell: it dispatches a local planning
// session and stays on the current tab (no tab switch).
func TestPickerChordPlanningClaude(t *testing.T) {
	m := testModel()
	m.openPicker("a", "epic")
	next, _ := m.handleKey(keyMsg("p"))
	m = next.(model)
	next, cmd := m.handleKey(keyMsg("l"))
	m = next.(model)
	require.False(t, m.pickerOpen)
	require.Equal(t, tabDetails, m.tab, "planning stays on the current tab")
	require.NotNil(t, cmd, "dispatch issues a planning command")
}

// Arrow navigation moves both axes; enter dispatches the armed cell. Down moves
// to the planning row and right to the codex column before enter fires.
func TestPickerArrowNavAndEnter(t *testing.T) {
	m := testModel()
	m.openPicker("a", "epic")

	next, _ := m.handleKey(keyMsg("down"))
	m = next.(model)
	require.Equal(t, pickPlanning, m.pickerMode)

	next, _ = m.handleKey(keyMsg("right"))
	m = next.(model)
	require.Equal(t, pickCodex, m.pickerBackend)

	next, cmd := m.handleKey(keyMsg("enter"))
	m = next.(model)
	require.False(t, m.pickerOpen, "enter dispatches and closes")
	require.NotNil(t, cmd)
}

// The planning prompt tells the session to plan via bd and forbids implementation.
func TestBuildPlanningPrompt(t *testing.T) {
	epic := buildPlanningPrompt("ep-1", "epic", "Platform", "/root")
	require.Contains(t, epic, "planning, not implementing")
	require.Contains(t, epic, "-C /root")
	require.Contains(t, epic, "ep-1")
	require.NotContains(t, epic, "pull request", "planning never opens a PR")

	task := buildPlanningPrompt("bd-1", "task", "Add cache", "/root")
	require.Contains(t, task, "bd-1")
	require.Contains(t, task, "Add cache")
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
