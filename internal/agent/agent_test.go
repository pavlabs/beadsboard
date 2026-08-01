package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeCommenter records the bead-activity comments a Manager posts.
type fakeCommenter struct {
	mu    sync.Mutex
	posts []string // "<beadID> <body>"
}

func (f *fakeCommenter) Comment(_ context.Context, id, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, id+" "+body)
	return nil
}

func (f *fakeCommenter) all() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.posts...)
}

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
	// finalize sets the terminal status before removing the worktree, so wait for
	// the removal rather than racing it.
	require.Eventually(t, func() bool { return liveWorktrees(t, repo) == 0 },
		2*time.Second, 20*time.Millisecond, "worktree removed on clean exit")
}

// A configured commenter receives spawn, session, and finish milestones on the
// bead's timeline, each with the parseable bb-agent prefix.
func TestSpawnPostsLifecycleComments(t *testing.T) {
	repo := gitRepo(t)
	bin := stubClaude(
		t,
		`{"type":"system","subtype":"init","session_id":"sess-123"}`,
		`{"type":"result","result":"All done."}`,
	)
	m := newAt(repo, bin, 10, t.TempDir())
	fc := &fakeCommenter{}
	m.SetCommenter(fc)

	_, err := m.Spawn(Spec{IssueID: "epic-x", Scope: "epic", Prompt: "go", PermissionMode: "acceptEdits"})
	require.NoError(t, err)
	waitTerminal(t, m)

	require.Eventually(t, func() bool { return len(fc.all()) >= 3 }, 5*time.Second, 20*time.Millisecond)
	joined := strings.Join(fc.all(), "\n")
	require.Contains(t, joined, "epic-x bb-agent spawn agent=epic-x-1 tool=claude mode=coding branch=beadsboard/epic-x-1")
	require.Contains(t, joined, "epic-x bb-agent session agent=epic-x-1 session=sess-123")
	require.Contains(t, joined, "epic-x bb-agent finish agent=epic-x-1 status=done branch=beadsboard/epic-x-1 result=All done.")
	require.NotContains(t, joined, "pr=", "an agent that opened no PR still posts a usable finish comment")
}

// The finish comment is the bead's link to the durable artifact, so a PR the
// agent announced has to reach the timeline alongside the branch.
func TestFinishCommentCarriesPRLink(t *testing.T) {
	repo := gitRepo(t)
	bin := stubClaude(
		t,
		`{"type":"result","result":"Opened https://github.com/pavlabs/beadsboard/pull/43 for review."}`,
	)
	m := newAt(repo, bin, 10, t.TempDir())
	fc := &fakeCommenter{}
	m.SetCommenter(fc)

	_, err := m.Spawn(Spec{IssueID: "task-9", Scope: "task", Prompt: "go", PermissionMode: "acceptEdits"})
	require.NoError(t, err)
	waitTerminal(t, m)

	require.Eventually(t, func() bool {
		return strings.Contains(strings.Join(fc.all(), "\n"), "finish")
	}, 5*time.Second, 20*time.Millisecond)
	require.Contains(t, strings.Join(fc.all(), "\n"),
		"task-9 bb-agent finish agent=task-9-1 status=done branch=beadsboard/task-9-1 "+
			"pr=https://github.com/pavlabs/beadsboard/pull/43 result=Opened")
}

// Codex reports narration and its final answer through the same result events, so
// a PR announced mid-run must not be lost by the results that follow it.
func TestPRLinkSurvivesLaterResults(t *testing.T) {
	m := New(t.TempDir(), "claude", 1)
	a := &agent{View: View{ID: "a1"}, backend: claudeBackend{bin: "claude"}}

	m.ingest(a, []byte(`{"type":"result","result":"Pushed, PR at https://github.com/o/r/pull/7"}`))
	m.ingest(a, []byte(`{"type":"result","result":"Cleaned up the worktree."}`))

	require.Equal(t, "https://github.com/o/r/pull/7", a.pullRequest)
}

// Only a real pull-request link may be posted as one: the result text is agent
// prose and mentions plenty of other URLs.
func TestPRURL(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"github pr", "see https://github.com/pavlabs/beadsboard/pull/43 thanks", "https://github.com/pavlabs/beadsboard/pull/43"},
		{"enterprise host", "https://git.corp.example.com:8443/team/app/pull/1", "https://git.corp.example.com:8443/team/app/pull/1"},
		{"trailing punctuation", "opened https://github.com/o/r/pull/9.", "https://github.com/o/r/pull/9"},
		{"issue link is not a pr", "closes https://github.com/o/r/issues/12", ""},
		{"no link", "All done.", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, prURL(tt.text))
		})
	}
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

// With Spec.RepoDir set, the worktree is cut from that sub-repo, not the
// manager's root — the meta-repo routing.
func TestSpawnWorktreesFromRepoDir(t *testing.T) {
	root := gitRepo(t)
	sub := gitRepo(t)
	bin := stubClaude(t, `{"type":"result","result":"done"}`)
	m := newAt(root, bin, 10, t.TempDir())

	_, err := m.Spawn(Spec{IssueID: "web-1", Prompt: "go", PermissionMode: "acceptEdits", RepoDir: sub})
	require.NoError(t, err)
	require.Equal(t, 1, liveWorktrees(t, sub), "worktree lives in the sub-repo")
	require.Equal(t, 0, liveWorktrees(t, root), "not in the root repo")

	waitTerminal(t, m)
	require.Eventually(t, func() bool { return liveWorktrees(t, sub) == 0 },
		2*time.Second, 20*time.Millisecond, "cleaned from the sub-repo on exit")
}

// claudeBackend.Parse folds a stream-json line into a normalized Event: the
// session id from the init line, an assistant progress note (text + tools), the
// final result, and nothing from a non-JSON line.
func TestClaudeParse(t *testing.T) {
	b := claudeBackend{bin: "claude"}
	tests := []struct {
		name string
		line string
		want Event
		ok   bool
	}{
		{"init carries session", `{"type":"system","subtype":"init","session_id":"sess-1"}`, Event{Session: "sess-1"}, true},
		{"assistant text and tool", `{"type":"assistant","message":{"content":[{"type":"text","text":"Looking"},{"type":"tool_use","name":"Read"}]}}`, Event{Progress: "Looking  → Read"}, true},
		{"result text", `{"type":"result","result":"All done."}`, Event{Result: "All done."}, true},
		{"non-json ignored", `not json`, Event{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, ok := b.Parse([]byte(tt.line))
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.want, ev)
		})
	}
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

// A finished agent's worktree is deleted, and the backend keys its session store
// by working directory — so resuming from anywhere else finds no session. Refuse
// rather than open a pane that cannot work.
func TestIntervenNeedsAWorktree(t *testing.T) {
	m := New(t.TempDir(), "claude", 2)
	a := &agent{View: View{ID: "a1", IssueID: "bd-1", Session: "sess-1", Status: NeedsInput}}
	a.worktree = t.TempDir()
	a.worktreePresent = true
	m.agents = append(m.agents, a)

	cwd, sess, err := m.Intervene("a1")
	require.NoError(t, err)
	require.Equal(t, a.worktree, cwd)
	require.Equal(t, "sess-1", sess)

	// The flag says present, but the directory is gone (finished agent, or a
	// reaped $TMPDIR).
	require.NoError(t, os.RemoveAll(a.worktree))
	_, _, err = m.Intervene("a1")
	require.ErrorIs(t, err, ErrWorktreeGone)

	// Cleanup already cleared the flag.
	a.worktreePresent = false
	_, _, err = m.Intervene("a1")
	require.ErrorIs(t, err, ErrWorktreeGone)
}

// Distinguish "not resumable yet" from "not resumable any more", so the board can
// say which.
func TestIntervenReportsWhy(t *testing.T) {
	m := New(t.TempDir(), "claude", 2)
	m.agents = append(m.agents, &agent{View: View{ID: "a2", IssueID: "bd-2", Status: Running}})

	_, _, err := m.Intervene("a2")
	require.ErrorIs(t, err, ErrNoSession)

	_, _, err = m.Intervene("nope")
	require.ErrorIs(t, err, ErrUnknownAgent)
}
