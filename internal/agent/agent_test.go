package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// gitRepo makes a throwaway repo with one commit so worktrees can be added.
func gitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "tester"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		require.NoError(t, cmd.Run(), "git %v", args)
	}
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644))
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		require.NoError(t, cmd.Run(), "git %v", args)
	}
	return repo
}

// stubClaude writes a fake `claude` that emits the given stream-json lines.
func stubClaude(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	for _, l := range lines {
		b.WriteString("printf '%s\\n' " + shellQuote(l) + "\n")
	}
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o755))
	return path
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func waitTerminal(t *testing.T, m *Manager) View {
	t.Helper()
	var v View
	require.Eventually(t, func() bool {
		s := m.Snapshot()
		if len(s) == 0 {
			return false
		}
		v = s[0]
		return v.Status != Running
	}, 5*time.Second, 20*time.Millisecond)
	return v
}

func liveWorktrees(t *testing.T, repo string) int {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").CombinedOutput()
	require.NoError(t, err)
	return strings.Count(string(out), "beadsboard/")
}

// A clean run captures the session id, ends Done, and its worktree is removed.
func TestSpawnDoneCapturesSessionAndCleansWorktree(t *testing.T) {
	repo := gitRepo(t)
	bin := stubClaude(
		t,
		`{"type":"system","subtype":"init","session_id":"sess-123"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Looking"},{"type":"tool_use","name":"Read"}]}}`,
		`{"type":"result","result":"All done."}`,
	)
	m := newAt(repo, bin, 10, t.TempDir())

	_, err := m.Spawn(Spec{IssueID: "epic-x", Scope: "epic", Prompt: "go", PermissionMode: "acceptEdits"})
	require.NoError(t, err)

	v := waitTerminal(t, m)
	require.Equal(t, Done, v.Status)
	require.Equal(t, "sess-123", v.Session)
	require.Equal(t, "beadsboard/epic-x-1", v.Branch)
	require.Contains(t, strings.Join(v.Tail, " "), "→ Read")
	require.Equal(t, 0, liveWorktrees(t, repo), "worktree removed on clean exit")
}

// A run that ends with the marker becomes NeedsInput, keeps its question, and
// keeps its worktree for resume.
func TestSpawnNeedsInputKeepsWorktree(t *testing.T) {
	repo := gitRepo(t)
	bin := stubClaude(
		t,
		`{"type":"system","session_id":"sess-9"}`,
		`{"type":"result","result":"Blocked. ⟨NEEDS INPUT⟩ Which region should I deploy to?"}`,
	)
	m := newAt(repo, bin, 10, t.TempDir())

	_, err := m.Spawn(Spec{IssueID: "task-7", Scope: "task", Prompt: "go", PermissionMode: "acceptEdits"})
	require.NoError(t, err)

	v := waitTerminal(t, m)
	require.Equal(t, NeedsInput, v.Status)
	require.Equal(t, "Which region should I deploy to?", v.Question)
	require.Equal(t, 1, liveWorktrees(t, repo), "worktree kept for intervene")

	m.Dismiss(v.ID)
	require.Eventually(t, func() bool { return len(m.Snapshot()) == 0 }, 2*time.Second, 20*time.Millisecond)
	require.Equal(t, 0, liveWorktrees(t, repo), "dismiss cleans the worktree")
}

// The concurrency cap rejects spawns past the limit.
func TestSpawnRespectsMaxAgents(t *testing.T) {
	repo := gitRepo(t)
	bin := stubClaude(t, `{"type":"result","result":"⟨NEEDS INPUT⟩ hold"}`) // stays Active (NeedsInput)
	m := newAt(repo, bin, 1, t.TempDir())

	_, err := m.Spawn(Spec{IssueID: "a", Prompt: "go", PermissionMode: "acceptEdits"})
	require.NoError(t, err)
	waitTerminal(t, m) // becomes NeedsInput, still counts as active

	_, err = m.Spawn(Spec{IssueID: "b", Prompt: "go", PermissionMode: "acceptEdits"})
	require.Error(t, err, "second spawn exceeds max=1")
}
