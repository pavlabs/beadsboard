// Package agent runs headless Claude Code processes as background workers, each
// scoped to a beads issue and isolated in its own git worktree. It captures the
// resumable session id, tails structured progress, and detects when an agent
// stops to ask for input. Durable outcomes live in beads, not here; logs are
// ephemeral and removed when an agent exits.
package agent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
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
	Repo           string // GITHUB_REPOSITORY for the agent's own bd/gh, or ""
	RepoDir        string // git repo to worktree from; "" = the manager's root repo
}

// View is an immutable snapshot of an agent for rendering.
type View struct {
	ID       string
	IssueID  string
	Scope    string
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
	worktree        string
	repoDir         string // the git repo its worktree was cut from
	cmd             *exec.Cmd
	cancel          context.CancelFunc
	tail            []string
	pendingResult   string
	killIntent      bool
	intervened      bool
	worktreePresent bool
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

func (a *agent) push(s string) {
	a.tail = append(a.tail, s)
	if len(a.tail) > 200 {
		a.tail = a.tail[len(a.tail)-200:]
	}
	a.Summary = s
}

// Manager owns all running agents and the worktree/log scratch space. It is safe
// for concurrent use; the UI reads snapshots and reacts to Events.
type Manager struct {
	mu        sync.Mutex
	repoDir   string
	claudeBin string
	maxAgents int
	logDir    string
	wtDir     string
	seq       int
	agents    []*agent
	events    chan struct{}
}

// New builds a Manager for repoDir. claudeBin is the Claude Code executable
// (overridable in tests); maxAgents caps concurrent live agents.
func New(repoDir, claudeBin string, maxAgents int) *Manager {
	return newAt(repoDir, claudeBin, maxAgents, filepath.Join(os.TempDir(), "beadsboard"))
}

func newAt(repoDir, claudeBin string, maxAgents int, base string) *Manager {
	m := &Manager{
		repoDir:   repoDir,
		claudeBin: claudeBin,
		maxAgents: maxAgents,
		logDir:    filepath.Join(base, "logs"),
		wtDir:     filepath.Join(base, "wt"),
		events:    make(chan struct{}, 8),
	}
	_ = os.MkdirAll(m.logDir, 0o755)
	_ = os.MkdirAll(m.wtDir, 0o755)
	return m
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

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, m.claudeBin, claudeArgs(spec)...)
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
		return View{}, fmt.Errorf("start claude: %w", err)
	}

	a := &agent{
		View: View{
			ID: id, IssueID: spec.IssueID, Scope: spec.Scope,
			Status: Running, Branch: branch, Started: time.Now(),
		},
		worktree: wt, repoDir: srcRepo, cmd: cmd, cancel: cancel, worktreePresent: true,
	}
	m.mu.Lock()
	m.agents = append(m.agents, a)
	view := a.snapshot()
	m.mu.Unlock()

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
	ev, ok := decode(line)
	if !ok {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if sid := sessionID(ev); sid != "" && a.Session == "" {
		a.Session = sid
	}
	switch ev["type"] {
	case "assistant":
		if t := assistantText(ev); t != "" {
			a.push(t)
		}
	case "result":
		if r := resultText(ev); r != "" {
			a.pendingResult = r
			a.Summary = firstLine(r)
		}
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
		a.Question = extractQuestion(a.pendingResult)
	case waitErr != nil:
		a.Status = Failed
		if a.Summary == "" {
			a.Summary = firstLine(waitErr.Error())
		}
	default:
		a.Status = Done
	}
	a.Ended = time.Now()
	keep := a.Status == NeedsInput || a.Status == Intervened
	if !keep {
		a.worktreePresent = false
	}
	wt, repoDir := a.worktree, a.repoDir
	m.mu.Unlock()

	_ = os.Remove(logPath) // logs are ephemeral; the question/outcome is kept in memory
	if !keep {
		m.removeWorktree(repoDir, wt)
	}
	m.ping()
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
func (m *Manager) Intervene(id string) (cwd, session string, ok bool) {
	m.mu.Lock()
	a := m.find(id)
	if a == nil || a.Session == "" {
		m.mu.Unlock()
		return "", "", false
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
	return wt, sess, true
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

func (m *Manager) removeWorktree(repoDir, path string) {
	_ = exec.Command("git", "-C", repoDir, "worktree", "remove", "--force", path).Run()
}

func claudeArgs(spec Spec) []string {
	args := []string{
		"-p", spec.Prompt,
		"--output-format", "stream-json", "--verbose",
		"--permission-mode", spec.PermissionMode,
	}
	if len(spec.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(spec.AllowedTools, ","))
	}
	if spec.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(spec.MaxTurns))
	}
	return args
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
