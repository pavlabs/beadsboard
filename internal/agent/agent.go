// Package agent runs headless Claude Code processes as background workers, each
// scoped to a beads issue and isolated in its own git worktree. It captures the
// resumable session id, tails structured progress, and detects when an agent
// stops to ask for input. Durable outcomes live in beads, not here; logs are
// ephemeral and removed when an agent exits.
package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pavlabs/beadsboard/internal/agentreg"
	"github.com/pavlabs/beadsboard/internal/beads"
)

// NeedsInputMarker is the sentinel the spawn prompt asks the agent to emit when
// it is blocked or unsure, so a run that ends this way is surfaced as a prompt
// for the user rather than a completion.
const NeedsInputMarker = "⟨NEEDS INPUT⟩"

// Status is an agent's lifecycle state.
type Status int

const (
	Running    Status = iota // process alive
	NeedsInput               // exited asking the user something
	Intervened               // handed off to an interactive session
	Done                     // finished cleanly
	Failed                   // process errored
	Killed                   // killed by the user
)

// Active agents are still the user's concern (running or awaiting them); Recent
// agents are finished and eligible to be pruned after a grace period.
func (s Status) Active() bool { return s == Running || s == NeedsInput || s == Intervened }
func (s Status) Recent() bool { return s == Done || s == Failed || s == Killed }

// Spec parameterizes a spawn. The caller supplies the prompt and the
// config-derived limits so this package stays UI- and config-agnostic.
type Spec struct {
	IssueID        string
	Scope          string // "task" | "epic"
	Prompt         string
	MaxTurns       int // 0 = uncapped
	PermissionMode string
	AllowedTools   []string
	Repo           string        // GITHUB_REPOSITORY for the agent's own bd/gh, or ""
	RepoDir        string        // git repo to worktree from; "" = the manager's root repo
	Tool           agentreg.Tool // backend; "" defaults to claude
}

// View is an immutable snapshot of an agent for rendering.
type View struct {
	ID       string
	IssueID  string
	Scope    string
	Tool     agentreg.Tool // backend that ran it, mirrors agentreg.Record.Tool
	Status   Status
	Question string
	Summary  string
	Session  string
	Branch   string
	Started  time.Time
	Ended    time.Time
	Tail     []string
}

type agent struct {
	View
	backend         Backend
	worktree        string
	repoDir         string // the git repo its worktree was cut from
	baseCommit      string // HEAD the worktree was cut from; used to detect landed commits
	repo            string // GitHub owner/name used to look up a PR for Branch
	cmd             *exec.Cmd
	cancel          context.CancelFunc
	tail            []string
	pendingResult   string
	killIntent      bool
	intervened      bool
	worktreePresent bool
	rec             agentreg.Record // this agent's registry entry
}

func (a *agent) snapshot() View {
	v := a.View
	tail := a.tail
	if len(tail) > 60 {
		tail = tail[len(tail)-60:]
	}
	v.Tail = append([]string(nil), tail...)
	return v
}

// push records a line of the agent's output. The text is sanitized here because
// an agent quotes what it reads — a diff, an issue body, a fetched file — and all
// of it lands in the TUI, where a stray escape sequence would drive the terminal.
func (a *agent) push(s string) {
	s = beads.Sanitize(s)
	a.tail = append(a.tail, s)
	if len(a.tail) > 200 {
		a.tail = a.tail[len(a.tail)-200:]
	}
	a.Summary = s
}

// Commenter posts a comment to a bead. beads.Client satisfies it; the Manager
// records agent lifecycle milestones on the bead's timeline through it, so the
// interface lives consumer-side and keeps agent decoupled from the beads package.
type Commenter interface {
	Comment(ctx context.Context, id, body string) error
}

// PullRequestFinder discovers open pull requests. beads.Client satisfies this
// alongside Commenter, letting the manager detect artifacts instead of relying
// on an agent to mention them in its final prose.
type PullRequestFinder interface {
	PullRequests(ctx context.Context, repos []string) ([]beads.PullRequest, error)
}

// Manager owns all running agents and the worktree/log scratch space. It is safe
// for concurrent use; the UI reads snapshots and reacts to Events.
type Manager struct {
	mu        sync.Mutex
	repoDir   string
	backends  map[string]Backend
	maxAgents int
	logDir    string
	wtDir     string
	seq       int
	agents    []*agent
	events    chan struct{}
	reg       *agentreg.Registry // shared .beadsboard/agents registry; nil if unavailable
	commenter Commenter          // posts lifecycle milestones to the bead; nil disables it
	pulls     PullRequestFinder  // discovers a PR for a finished agent's branch
}

// New builds a Manager for repoDir. claudeBin is the Claude Code executable
// (overridable in tests); maxAgents caps concurrent live agents.
func New(repoDir, claudeBin string, maxAgents int) *Manager {
	return newAt(repoDir, claudeBin, maxAgents, filepath.Join(os.TempDir(), "beadsboard"))
}

func newAt(repoDir, claudeBin string, maxAgents int, base string) *Manager {
	m := &Manager{
		repoDir: repoDir,
		backends: map[string]Backend{
			string(agentreg.ToolClaude): claudeBackend{bin: claudeBin},
			string(agentreg.ToolCodex):  codexBackend{bin: "codex"},
			string(agentreg.ToolOllama): ollamaBackend{bin: "ollama"},
		},
		maxAgents: maxAgents,
		logDir:    filepath.Join(base, "logs"),
		wtDir:     filepath.Join(base, "wt"),
		events:    make(chan struct{}, 8),
	}
	_ = os.MkdirAll(m.logDir, 0o755)
	_ = os.MkdirAll(m.wtDir, 0o755)
	// The registry lives at the beads root, not the scratch base. It's lazy —
	// the on-disk dir appears only when the first agent is registered.
	m.reg = agentreg.New(repoDir)
	return m
}

// regPut, regRemove, regReap are nil-tolerant wrappers so registry I/O failures
// never break agent lifecycle. They run outside m.mu (disk I/O off the hot path).
func (m *Manager) regPut(rec agentreg.Record) {
	if m.reg != nil {
		_ = m.reg.Put(rec)
	}
}

func (m *Manager) regRemove(id string) {
	if m.reg != nil {
		_ = m.reg.Remove(id)
	}
}

func (m *Manager) regReap() {
	if m.reg != nil {
		_, _ = m.reg.Reap()
	}
}

// SetCommenter wires the sink for bead-activity comments. Optional: a nil
// commenter (the default) silently disables timeline posting.
func (m *Manager) SetCommenter(c Commenter) {
	m.commenter = c
	m.pulls = nil
	if pulls, ok := c.(PullRequestFinder); ok {
		m.pulls = pulls
	}
}

// comment posts a bead-activity line best-effort, off the caller's goroutine, so
// an agent's lifecycle never blocks on or fails because of a comment error.
func (m *Manager) comment(beadID, body string) {
	if m.commenter == nil || beadID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = m.commenter.Comment(ctx, beadID, body)
}

// Events fires whenever agent state changes in a way the UI should reflect.
func (m *Manager) Events() <-chan struct{} { return m.events }

// SetMaxAgents applies a new concurrency cap (e.g. after a config reload).
func (m *Manager) SetMaxAgents(n int) {
	m.mu.Lock()
	m.maxAgents = n
	m.mu.Unlock()
}

func (m *Manager) ping() {
	select {
	case m.events <- struct{}{}:
	default:
	}
}

// Sweep clears scratch space left by a prior run and prunes dangling worktree
// registrations. Safe only at startup, when no agents are live.
func (m *Manager) Sweep() {
	_ = os.RemoveAll(m.logDir)
	_ = os.RemoveAll(m.wtDir)
	_ = os.MkdirAll(m.logDir, 0o755)
	_ = os.MkdirAll(m.wtDir, 0o755)
	_ = exec.Command("git", "-C", m.repoDir, "worktree", "prune").Run()
	// Drop only registry records whose process is gone (crashed prior runs),
	// leaving agents of a concurrent beadsboard instance untouched.
	m.regReap()
}

// Spawn starts a headless agent for spec in a fresh worktree.
func (m *Manager) Spawn(spec Spec) (View, error) {
	m.mu.Lock()
	live := 0
	for _, a := range m.agents {
		if a.Status.Active() {
			live++
		}
	}
	if m.maxAgents > 0 && live >= m.maxAgents {
		max := m.maxAgents
		m.mu.Unlock()
		return View{}, fmt.Errorf("agent limit reached: max=%d", max)
	}
	m.seq++
	id := fmt.Sprintf("%s-%d", shortIssue(spec.IssueID), m.seq)
	m.mu.Unlock()

	// The worktree is cut from the bead's sub-repo when routed there, else the
	// manager's root repo — the single-repo default.
	srcRepo := spec.RepoDir
	if srcRepo == "" {
		srcRepo = m.repoDir
	}
	baseCommit, err := gitOutput(srcRepo, "rev-parse", "HEAD")
	if err != nil {
		return View{}, fmt.Errorf("resolve worktree base: %w", err)
	}
	wt := filepath.Join(m.wtDir, id)
	branch := "beadsboard/" + id
	if err := m.addWorktree(srcRepo, branch, wt); err != nil {
		return View{}, err
	}

	logPath := filepath.Join(m.logDir, id+".jsonl")
	logFile, err := os.Create(logPath)
	if err != nil {
		m.removeWorktree(srcRepo, wt)
		return View{}, fmt.Errorf("create log: %w", err)
	}

	tool := spec.Tool
	if tool == "" {
		tool = agentreg.ToolClaude
	}
	b := m.backendFor(tool)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, b.Bin(), b.HeadlessArgs(spec)...)
	cmd.Dir = wt
	if spec.Repo != "" {
		cmd.Env = append(os.Environ(), "GITHUB_REPOSITORY="+spec.Repo)
	}
	cmd.Stderr = logFile
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = logFile.Close()
		m.cleanupSpawn(srcRepo, logPath, wt)
		return View{}, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		_ = logFile.Close()
		m.cleanupSpawn(srcRepo, logPath, wt)
		return View{}, fmt.Errorf("start agent: %w", err)
	}

	a := &agent{
		View: View{
			ID: id, IssueID: spec.IssueID, Scope: spec.Scope, Tool: tool,
			Status: Running, Branch: branch, Started: time.Now(),
		},
		backend:  b,
		worktree: wt, repoDir: srcRepo, baseCommit: baseCommit, repo: spec.Repo,
		cmd: cmd, cancel: cancel, worktreePresent: true,
	}
	a.rec = agentreg.Record{
		ID: id, BeadID: spec.IssueID, Tool: tool, Mode: agentreg.ModeCoding,
		PID: cmd.Process.Pid, Cwd: wt, Branch: branch,
		Source: agentreg.SourceBeadsboard, StartedAt: a.Started,
	}
	m.mu.Lock()
	m.agents = append(m.agents, a)
	view := a.snapshot()
	rec := a.rec
	m.mu.Unlock()
	m.regPut(rec)
	go m.comment(rec.BeadID, spawnComment(rec))

	go m.run(a, stdout, logFile, logPath)
	m.ping()
	return view, nil
}

func (m *Manager) cleanupSpawn(repoDir, logPath, wt string) {
	_ = os.Remove(logPath)
	m.removeWorktree(repoDir, wt)
}

func (m *Manager) run(a *agent, stdout io.Reader, logFile *os.File, logPath string) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := append([]byte(nil), sc.Bytes()...)
		_, _ = logFile.Write(append(line, '\n'))
		m.ingest(a, line)
	}
	if err := sc.Err(); err != nil {
		m.mu.Lock()
		a.push("stream error: " + err.Error())
		m.mu.Unlock()
	}
	waitErr := a.cmd.Wait()
	_ = logFile.Close()
	m.finalize(a, waitErr, logPath)
}

func (m *Manager) ingest(a *agent, line []byte) {
	ev, ok := a.backend.Parse(line)
	if !ok {
		return
	}
	m.mu.Lock()
	var captured agentreg.Record
	if ev.Session != "" && a.Session == "" {
		a.Session = ev.Session
		a.rec.SessionID = ev.Session
		captured = a.rec
	}
	if ev.Progress != "" {
		a.push(ev.Progress)
	}
	if ev.Result != "" {
		a.pendingResult = ev.Result
		a.Summary = beads.Sanitize(firstLine(ev.Result))
	}
	m.mu.Unlock()
	// Persist the session id once, so a rediscovered agent stays resumable.
	if captured.ID != "" {
		m.regPut(captured)
		go m.comment(captured.BeadID, sessionComment(captured))
	}
}

func (m *Manager) finalize(a *agent, waitErr error, logPath string) {
	m.mu.Lock()
	switch {
	case a.intervened:
		a.Status = Intervened
	case a.killIntent:
		a.Status = Killed
	case strings.Contains(a.pendingResult, NeedsInputMarker):
		a.Status = NeedsInput
		a.Question = beads.Sanitize(extractQuestion(a.pendingResult))
	case waitErr != nil:
		a.Status = Failed
		if a.Summary == "" {
			a.Summary = beads.Sanitize(firstLine(waitErr.Error()))
		}
	default:
		a.Status = Done
	}
	a.Ended = time.Now()
	keep := a.Status == NeedsInput || a.Status == Intervened
	if !keep {
		a.worktreePresent = false
	}
	wt, repoDir, id := a.worktree, a.repoDir, a.ID
	beadID, status, result := a.IssueID, a.Status, a.pendingResult
	baseCommit, branch, repo := a.baseCommit, a.Branch, a.repo
	m.mu.Unlock()

	landedBranch, pr := m.detectArtifacts(wt, baseCommit, branch, repo)

	// The headless process has exited, so drop its registry record regardless of
	// outcome; a needs-input agent stays visible via the in-memory Snapshot.
	m.regRemove(id)
	go m.comment(beadID, finishComment(id, status, landedBranch, pr, result))
	_ = os.Remove(logPath) // logs are ephemeral; the question/outcome is kept in memory
	if !keep {
		m.removeWorktree(repoDir, wt)
	}
	m.ping()
}

// detectArtifacts runs while the worktree still exists. A branch is durable
// work only when it has commits beyond the spawn-time HEAD; a PR is learned
// from GitHub by matching that branch, never by scraping agent-authored prose.
func (m *Manager) detectArtifacts(worktree, baseCommit, branch, repo string) (string, string) {
	if baseCommit == "" || branch == "" {
		return "", ""
	}
	count, err := gitOutput(worktree, "rev-list", "--count", baseCommit+"..HEAD")
	if err != nil || count == "0" {
		return "", ""
	}
	if m.pulls == nil || repo == "" {
		return branch, ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pulls, err := m.pulls.PullRequests(ctx, []string{repo})
	if err != nil {
		return branch, ""
	}
	for _, pull := range pulls {
		if !pull.Fork && pull.Repo == repo && pull.Branch == branch {
			return branch, pull.URL
		}
	}
	return branch, ""
}

// Kill terminates a running agent; it moves to Killed. No-op for agents that are
// no longer running.
func (m *Manager) Kill(id string) {
	m.mu.Lock()
	a := m.find(id)
	if a == nil || a.Status != Running {
		m.mu.Unlock()
		return
	}
	a.killIntent = true
	cancel := a.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel() // run goroutine finalizes as Killed
	}
}

// Intervene kills the headless process but keeps its worktree, returning the
// working directory and session id so the caller can resume it interactively.
// ErrNoSession and ErrWorktreeGone are why an agent can't be resumed. A session
// id only exists once the backend has emitted one, and the worktree is the
// directory the backend keys its session store by — resuming from anywhere else
// finds no session — so a finished agent whose worktree was cleaned up cannot be
// resumed at all.
var (
	ErrNoSession    = errors.New("no session captured yet")
	ErrWorktreeGone = errors.New("worktree is gone, so its session can't be found")
	ErrUnknownAgent = errors.New("agent is not tracked by this board")
)

func (m *Manager) Intervene(id string) (cwd, session string, err error) {
	m.mu.Lock()
	a := m.find(id)
	if a == nil {
		m.mu.Unlock()
		return "", "", ErrUnknownAgent
	}
	if a.Session == "" {
		m.mu.Unlock()
		return "", "", ErrNoSession
	}
	if !a.worktreePresent || !dirExists(a.worktree) {
		m.mu.Unlock()
		return "", "", ErrWorktreeGone
	}
	a.intervened = true
	cancel, wt, sess := a.cancel, a.worktree, a.Session
	running := a.Status == Running
	if !running {
		a.Status = Intervened // already exited (e.g. needs-input): just mark it
	}
	m.mu.Unlock()
	if running && cancel != nil {
		cancel()
	} else {
		m.ping()
	}
	return wt, sess, nil
}

// dirExists reports whether path is a directory that is still there. The
// worktree lives under $TMPDIR, which is reaped out from under a long-lived
// board, so the recorded flag alone is not proof.
func dirExists(path string) bool {
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// Dismiss removes an agent from the registry and cleans up any worktree it still
// holds.
func (m *Manager) Dismiss(id string) {
	m.mu.Lock()
	idx := m.index(id)
	if idx < 0 {
		m.mu.Unlock()
		return
	}
	a := m.agents[idx]
	wt, repoDir, present := a.worktree, a.repoDir, a.worktreePresent
	m.agents = append(m.agents[:idx], m.agents[idx+1:]...)
	m.mu.Unlock()
	m.regRemove(id)
	if present {
		m.removeWorktree(repoDir, wt)
	}
	m.ping()
}

// PruneRecent drops finished agents whose grace period has elapsed. Active
// agents (including needs-input) are never auto-pruned.
func (m *Manager) PruneRecent(ttl time.Duration) {
	cutoff := time.Now().Add(-ttl)
	m.mu.Lock()
	kept := m.agents[:0]
	changed := false
	for _, a := range m.agents {
		if a.Status.Recent() && a.Ended.Before(cutoff) {
			changed = true
			continue
		}
		kept = append(kept, a)
	}
	m.agents = kept
	m.mu.Unlock()
	if changed {
		m.ping()
	}
}

// Snapshot returns a render-safe copy of every agent, spawn order preserved.
func (m *Manager) Snapshot() []View {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]View, len(m.agents))
	for i, a := range m.agents {
		out[i] = a.snapshot()
	}
	return out
}

func (m *Manager) find(id string) *agent {
	if i := m.index(id); i >= 0 {
		return m.agents[i]
	}
	return nil
}

func (m *Manager) index(id string) int {
	for i, a := range m.agents {
		if a.ID == id {
			return i
		}
	}
	return -1
}

func (m *Manager) addWorktree(repoDir, branch, path string) error {
	cmd := exec.Command("git", "-C", repoDir, "worktree", "add", "-b", branch, path, "HEAD")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("worktree add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (m *Manager) removeWorktree(repoDir, path string) {
	_ = exec.Command("git", "-C", repoDir, "worktree", "remove", "--force", path).Run()
}

// shortIssue reduces an issue id to a filesystem- and branch-safe stem.
func shortIssue(id string) string {
	if i := strings.LastIndexAny(id, "/:"); i >= 0 {
		id = id[i+1:]
	}
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "agent"
	}
	return b.String()
}

// label is the lifecycle word used in bead-activity comments.
func (s Status) label() string {
	switch s {
	case Running:
		return "running"
	case NeedsInput:
		return "needs-input"
	case Intervened:
		return "intervened"
	case Done:
		return "done"
	case Failed:
		return "failed"
	case Killed:
		return "killed"
	}
	return "unknown"
}

// commentTag prefixes every bead-activity comment so a timeline can be parsed
// back out later: `bb-agent <verb> key=value …`.
const commentTag = "bb-agent"

func spawnComment(rec agentreg.Record) string {
	body := fmt.Sprintf("%s spawn agent=%s tool=%s mode=%s", commentTag, rec.ID, rec.Tool, rec.Mode)
	if rec.Branch != "" {
		body += " branch=" + rec.Branch
	}
	return body
}

func sessionComment(rec agentreg.Record) string {
	return fmt.Sprintf("%s session agent=%s session=%s", commentTag, rec.ID, rec.SessionID)
}

func finishComment(id string, status Status, branch, pr, result string) string {
	body := fmt.Sprintf("%s finish agent=%s status=%s", commentTag, id, status.label())
	if branch != "" {
		body += " branch=" + branch
	}
	if pr != "" {
		body += " pr=" + pr
	}
	if r := firstLine(result); r != "" {
		body += " result=" + r
	}
	return body
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// extractQuestion returns the agent's ask: the text following the needs-input
// marker, or the whole result if the marker sits at the end.
func extractQuestion(result string) string {
	before, after, found := strings.Cut(result, NeedsInputMarker)
	if !found {
		return firstLine(result)
	}
	if q := strings.TrimSpace(after); q != "" {
		return q
	}
	return strings.TrimSpace(before)
}
