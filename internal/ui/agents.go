package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/pavlabs/beadsboard/internal/agent"
	"github.com/pavlabs/beadsboard/internal/agentreg"
	"github.com/pavlabs/beadsboard/internal/beads"
	"github.com/pavlabs/beadsboard/internal/config"
)

// --- agent spawning & intervention --------------------------------------------

// spawnCmd launches a headless agent for the issue off the UI goroutine. With
// the GitHub plugin on it first ensures the bead has a linked issue (so the
// agent's PR can close it) and passes the repo into the agent's environment.
func (m model) spawnCmd(issueID, scope string, tool agentreg.Tool) tea.Cmd {
	title, ref := "", ""
	var labels []string
	if is, ok := m.graph.Issues[issueID]; ok {
		title, ref, labels = is.Title, is.ExternalRef, is.Labels
	}
	client, cfg, mgr := m.client, m.cfg, m.mgr
	beadsRoot := client.Dir
	return func() tea.Msg {
		// Route the bead to its repo: a repo::<name> label worktrees the sub-repo
		// and puts the issue there; unlabeled beads fall back to the root repo.
		target := client.RepoFor(labels, cfg.GitHubRepository)
		var syncErr error
		if cfg.GitHubSync {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			syncErr = client.EnsureIssue(ctx, issueID, ref, target.GitHub)
			cancel()
		}
		spec := agent.Spec{
			IssueID:        issueID,
			Scope:          scope,
			Tool:           tool,
			Prompt:         buildPrompt(issueID, scope, title, beadsRoot, cfg.GitHubSync, beads.GithubNumber(ref)),
			MaxTurns:       cfg.MaxTurns,
			PermissionMode: cfg.PermissionMode,
			AllowedTools:   cfg.AllowedTools(),
			RepoDir:        target.Dir, // worktree from the bead's repo (root when unlabeled)
		}
		if cfg.GitHubSync {
			spec.Repo = target.GitHub
		}
		_, err := mgr.Spawn(spec)
		if err == nil {
			err = syncErr // surface a best-effort sync failure only if the spawn itself succeeded
		}
		return spawnedMsg{err: err}
	}
}

// pullStatusesCmd makes GitHub authoritative over local bead status: it reads
// each linked issue's status (open/closed state + status:: label) and applies
// any that differs via `bd update`, off the UI goroutine. This is the reverse of
// the on-edit push — a teammate's change on GitHub (or the board, via the
// reverse Action that relabels the issue) flows back into bd here.
func (m model) pullStatusesCmd() tea.Cmd {
	client, cfg := m.client, m.cfg
	type target struct{ id, cur, ref string }
	var targets []target
	for id, is := range m.graph.Issues {
		if is.ExternalRef != "" {
			targets = append(targets, target{id: id, cur: is.Status, ref: is.ExternalRef})
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// With a board configured, cards drive status: read the Status column so a
		// card move flows back. Otherwise fall back to the issue's state + label.
		// Both are keyed by the issue URL, matching a bead's external_ref.
		var statuses map[string]string
		var err error
		if cfg.GitHubProjectNumber > 0 {
			statuses, err = client.BoardStatuses(ctx, cfg.GitHubProjectOwner, cfg.GitHubProjectNumber)
		} else {
			statuses, err = client.IssueStatuses(ctx, cfg.GitHubRepository)
		}
		if err != nil {
			return pulledMsg{err: err}
		}
		changed := 0
		for _, t := range targets {
			desired, ok := statuses[t.ref]
			if !ok || desired == "" || desired == t.cur {
				continue
			}
			if err := client.Update(ctx, t.id, "status", desired); err != nil {
				return pulledMsg{err: err}
			}
			changed++
		}
		return pulledMsg{changed: changed}
	}
}

// buildPrompt tells the agent to recall project context, do the scoped work on
// its isolated branch, and stop-and-ask (with the marker) rather than guess.
// When the GitHub plugin is on it also asks for a PR that closes the tracking
// issue: by number when known, else resolved by the agent from external_ref.
func buildPrompt(id, scope, title, beadsRoot string, ghSync bool, issueNum int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "This project's beads live at %s, OUTSIDE your worktree — prefix every bd command with `-C %s` (e.g. `bd -C %s show %s`, `bd -C %s update %s --status ...`).\n\n", beadsRoot, beadsRoot, beadsRoot, id, beadsRoot, id)
	fmt.Fprintf(&sb, "Recall the project: run `bd -C %s prime`, then `bd -C %s show %s` and read the issue in full.\n\n", beadsRoot, beadsRoot, id)
	if scope == "epic" {
		fmt.Fprintf(&sb, "Work through every open task in epic %s «%s» in dependency order. For each: implement it, run the project's checks, commit, and update its bd status. When the epic is complete, open a pull request for this branch.\n\n", id, title)
	} else {
		fmt.Fprintf(&sb, "Implement task %s «%s»: make the change, run the project's checks, commit on this branch, update its bd status, and open a pull request.\n\n", id, title)
	}
	if ghSync {
		if issueNum > 0 {
			fmt.Fprintf(&sb, "This work is tracked as GitHub issue #%d in this repo — include `Closes #%d` in the PR description so merging it closes the issue.\n\n", issueNum, issueNum)
		} else {
			fmt.Fprintf(&sb, "This work is tracked as a GitHub issue in this repo — find its number (`bd -C %s show %s` → external_ref URL, or `gh issue list`) and include `Closes #N` in the PR description so merging it closes the issue.\n\n", beadsRoot, id)
		}
	}
	fmt.Fprintf(&sb, "You are on an isolated git worktree and branch, so commit and push freely. If anything is ambiguous or you get blocked, do NOT guess — stop and ask: end your final message with the marker %s followed by your question.", agent.NeedsInputMarker)
	return sb.String()
}

// buildPlanningPrompt seeds an interactive planning session: shape the backlog
// via bd, no implementation. bd lives at beadsRoot so every command is -C'd.
func buildPlanningPrompt(id, scope, title, beadsRoot string) string {
	var sb strings.Builder
	sb.WriteString("You are planning, not implementing. Do NOT write code, commit, or open a PR.\n\n")
	fmt.Fprintf(&sb, "This project's beads live at %s — prefix every bd command with `-C %s` (e.g. `bd -C %s show %s`, `bd -C %s update %s ...`, `bd -C %s create ...`).\n\n", beadsRoot, beadsRoot, beadsRoot, id, beadsRoot, id, beadsRoot)
	fmt.Fprintf(&sb, "Recall the project: run `bd -C %s prime`, then `bd -C %s show %s` and read it in full.\n\n", beadsRoot, beadsRoot, id)
	if scope == "epic" {
		fmt.Fprintf(&sb, "Plan epic %s «%s»: break it into well-scoped tasks in dependency order, then create or update them as beads via `bd -C %s`.", id, title, beadsRoot)
	} else {
		fmt.Fprintf(&sb, "Plan task %s «%s»: sharpen its scope and acceptance, refine its description via `bd -C %s`, and split it into new beads if it is too big.", id, title, beadsRoot)
	}
	return sb.String()
}

// interveneCmd opens an interactive resume of the agent's session in a floating
// zellij pane, using the agent's own backend to build the resume command.
// Requires running inside a zellij session.
func interveneCmd(cwd, session string, b agent.Backend) tea.Cmd {
	return func() tea.Msg {
		resume := append([]string{b.Bin()}, b.ResumeArgs(session)...)
		if os.Getenv("ZELLIJ") == "" {
			return interveneMsg{err: fmt.Errorf("not in zellij — resume manually: cd %s && %s", cwd, strings.Join(resume, " "))}
		}
		// zellij drops the pane in $HOME rather than failing when --cwd does not
		// exist, and the backend then resumes from the wrong directory and reports
		// no such session. Refuse instead of opening a pane that cannot work.
		if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() {
			return interveneMsg{err: fmt.Errorf("worktree is gone: %s", cwd)}
		}
		name := "resume " + session
		if len(name) > 24 {
			name = name[:24]
		}
		args := append([]string{"run", "--floating", "--close-on-exit", "--name", name, "--cwd", cwd, "--"}, resume...)
		cmd := exec.Command("zellij", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return interveneMsg{err: fmt.Errorf("zellij: %w: %s", err, strings.TrimSpace(string(out)))}
		}
		return interveneMsg{}
	}
}

// tailLines renders the last log lines that fit in rows. Wrapping makes one log
// line occupy several rows, so the window is filled from the newest line
// backwards by rendered height rather than by line count.
func tailLines(tail []string, width, rows int, wrap bool) []string {
	if !wrap {
		if len(tail) > rows {
			tail = tail[len(tail)-rows:]
		}
		out := make([]string, len(tail))
		for i, l := range tail {
			out[i] = truncate(l, width)
		}
		return out
	}

	style := lipgloss.NewStyle().Width(max(width, 1))
	var out []string
	used := 0
	for i := len(tail) - 1; i >= 0; i-- {
		// Width() pads as well as wraps; trim it back so a wrapped row is the same
		// shape as an unwrapped one.
		block := strings.Split(style.Render(tail[i]), "\n")
		for j, row := range block {
			block[j] = strings.TrimRight(row, " ")
		}
		if used+len(block) > rows {
			// A block that only partly fits keeps its newest rows, so the most
			// recent line is never the one dropped.
			if room := rows - used; room > 0 && len(out) == 0 {
				out = append(block[len(block)-room:], out...)
			}
			break
		}
		out = append(block, out...)
		used += len(block)
	}
	return out
}

// planCmd opens an interactive planning session for a bead in a floating zellij
// pane rooted at the beads dir. Unlike a coding spawn it is local-only — no
// worktree, branch, or PR — the session just runs bd and shapes the backlog. It
// registers itself in the agent registry for the pane's lifetime (so the ledger
// shows it) and deregisters on exit. Requires running inside a zellij session.
func (m model) planCmd(target, scope string, tool agentreg.Tool) tea.Cmd {
	title := ""
	if is, ok := m.graph.Issues[target]; ok {
		title = is.Title
	}
	beadsRoot := m.client.Dir
	b := m.mgr.Backend(tool)
	prompt := buildPlanningPrompt(target, scope, title, beadsRoot)
	return func() tea.Msg {
		session := shQuote(b.Bin()) + " " + shQuote(prompt)
		if os.Getenv("ZELLIJ") == "" {
			return interveneMsg{err: fmt.Errorf("not in zellij — plan manually: cd %s && %s", beadsRoot, session)}
		}
		id := planSessionID(target)
		// Bracket the interactive session with register/unregister so the ledger
		// tracks it; $PWD is the pane's cwd (beadsRoot) and $$ its pid, for liveness.
		script := fmt.Sprintf(
			"beadsboard agent register --id %s --bead %s --mode planning --source beadsboard --tool %s --cwd \"$PWD\" --pid $$; %s; beadsboard agent unregister --id %s",
			shQuote(id), shQuote(target), shQuote(string(tool)), session, shQuote(id),
		)
		name := "plan " + target
		if len(name) > 24 {
			name = name[:24]
		}
		cmd := exec.Command("zellij", "run", "--floating", "--close-on-exit",
			"--name", name, "--cwd", beadsRoot, "--", "sh", "-c", script)
		if out, err := cmd.CombinedOutput(); err != nil {
			return interveneMsg{err: fmt.Errorf("zellij: %w: %s", err, strings.TrimSpace(string(out)))}
		}
		return interveneMsg{}
	}
}

// planSessionID is a registry-safe unique id for a planning pane: a sanitized
// bead id plus a timestamp, so concurrent plans on one bead don't collide.
func planSessionID(bead string) string {
	var sb strings.Builder
	sb.WriteString("plan-")
	for _, r := range bead {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			sb.WriteRune(r)
		default:
			sb.WriteByte('-')
		}
	}
	fmt.Fprintf(&sb, "-%d", time.Now().UnixNano())
	return sb.String()
}

// shQuote single-quotes s for safe interpolation into an `sh -c` script.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// --- Agents tab keys ----------------------------------------------------------

func (m model) handleAgentsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	agents := m.visibleAgents()
	switch msg.String() {
	case "m", "esc":
		m.tab = tabDetails
	case "A":
		m.showAll = !m.showAll
		m.clampAgentCursor()
	case "w":
		// Board-wide, not per agent: moving through the list must not reset it.
		m.wrapLogs = !m.wrapLogs
	case "S":
		m.openSettings()
	case "up":
		if m.agentCursor > 0 {
			m.agentCursor--
		}
	case "down":
		if m.agentCursor < len(agents)-1 {
			m.agentCursor++
		}
	case "k":
		if r, ok := m.selectedAgent(); ok {
			if !r.managed() {
				m.notice = notManagedHere
				return m, nil
			}
			if r.view.Status == agent.Running {
				m.mgr.Kill(r.id())
			}
		}
	case "x":
		if r, ok := m.selectedAgent(); ok {
			if !r.managed() {
				m.notice = notManagedHere
				return m, nil
			}
			m.mgr.Dismiss(r.id())
			m.clampAgentCursor()
		}
	case "enter":
		if r, ok := m.selectedAgent(); ok {
			if !r.managed() {
				m.notice = notManagedHere
				return m, nil
			}
			cwd, sess, err := m.mgr.Intervene(r.id())
			if err != nil {
				m.notice = "can't resume " + shortID(r.bead()) + ": " + err.Error()
				return m, nil
			}
			return m, interveneCmd(cwd, sess, m.mgr.Backend(r.tool()))
		}
	}
	return m, nil
}

// --- launcher matrix ----------------------------------------------------------

// handlePickerKey drives the launcher matrix (coding/planning × claude/codex).
// The mode letters c/p only move the row; the backend letters l/o pick the column
// AND dispatch, so a blind chord `a c l` / `a p o` completes on the tool letter.
// Horizontal nav is arrows-only — h/l would collide with the claude chord.
func (m model) handlePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.pickerOpen = false
	case "c":
		m.pickerMode = pickCoding
	case "p":
		m.pickerMode = pickPlanning
	case "l":
		m.pickerBackend = pickClaude
		return m.dispatchPicker()
	case "o":
		m.pickerBackend = pickCodex
		return m.dispatchPicker()
	case "up", "down", "j", "k":
		m.pickerMode = (m.pickerMode + 1) % 2 // two rows: toggle
	case "left", "right":
		m.pickerBackend = (m.pickerBackend + 1) % 2 // two columns: toggle
	case "enter":
		return m.dispatchPicker()
	}
	return m, nil
}

// dispatchPicker closes the picker and launches the armed cell: a coding cell
// spawns a headless agent and switches to the Agents tab; a planning cell opens
// an interactive local session, staying on the current tab (like intervene).
func (m model) dispatchPicker() (tea.Model, tea.Cmd) {
	target, scope := m.pickerTarget, m.pickerScope
	tool := pickerTools[m.pickerBackend]
	planning := m.pickerMode == pickPlanning
	m.pickerOpen = false
	if planning {
		return m, m.planCmd(target, scope, tool)
	}
	m.tab = tabAgents
	return m, m.spawnCmd(target, scope, tool)
}

// notManagedHere is shown for actions that need the in-process Manager. A
// registry row belongs to another board or an external session, so this board
// has its record but not its process or its log.
const notManagedHere = "not run by this board — kill or resume it where it started"

// agentRow is one agent as the board sees it, on both surfaces that list them —
// the Agents tab and a bead's ledger. It is backed by this board's own Manager
// (a live status and a log tail), known only from the shared on-disk registry
// (an externally registered session, another board's agent, or one of ours from
// before a restart), or both — a managed agent whose record supplies the mode
// and source its view has no field for. The registry is the reason the directory
// exists, so both surfaces have to read it rather than only this process's memory.
type agentRow struct {
	view  *agent.View      // nil for a registry-only row
	rec   *agentreg.Record // nil when the registry does not know this agent
	alive bool             // registry liveness; consulted only without a view
}

func (r agentRow) managed() bool { return r.view != nil }

func (r agentRow) id() string {
	if r.view != nil {
		return r.view.ID
	}
	return r.rec.ID
}

func (r agentRow) bead() string {
	if r.view != nil {
		return r.view.IssueID
	}
	return r.rec.BeadID
}

// tool, mode and source read the record first: it is the truth shared across
// boards, and carries what a view cannot say about itself.
func (r agentRow) tool() agentreg.Tool {
	if r.rec != nil {
		return r.rec.Tool
	}
	return r.view.Tool
}

func (r agentRow) mode() string {
	if r.rec != nil {
		return string(r.rec.Mode)
	}
	return "coding"
}

func (r agentRow) source() string {
	if r.rec != nil {
		return string(r.rec.Source)
	}
	return "local"
}

// active decides which half of the list a row sits in: a live process, or one
// whose work is over (or whose owner is gone).
func (r agentRow) active() bool {
	if r.view != nil {
		return r.view.Status.Active()
	}
	return r.alive
}

// statusWord labels the row: a managed row reports its process status, a
// registry row only whether the process is still there.
func (r agentRow) statusWord() string {
	if r.view != nil {
		return agentWord(r.view.Status)
	}
	if r.alive {
		return "running"
	}
	return "ended"
}

// agentRows merges this board's live agents with the cached registry records,
// deduped by ID (a record for a live agent enriches its row rather than adding a
// second line), keeping the rows keep accepts, active first then stable.
func (m model) agentRows(keep func(agentRow) bool) []agentRow {
	recByID := make(map[string]*agentreg.Record, len(m.agentRecords))
	for i := range m.agentRecords {
		recByID[m.agentRecords[i].ID] = &m.agentRecords[i]
	}

	var active, recent []agentRow
	add := func(r agentRow) {
		if !keep(r) {
			return
		}
		if r.active() {
			active = append(active, r)
		} else {
			recent = append(recent, r)
		}
	}
	managed := map[string]bool{}
	for _, v := range m.mgr.Snapshot() {
		managed[v.ID] = true
		add(agentRow{view: &v, rec: recByID[v.ID]})
	}
	// Registry records the Manager doesn't know: another board's agents, external
	// sessions, and our own from before a restart.
	for i := range m.agentRecords {
		rec := &m.agentRecords[i]
		if !managed[rec.ID] {
			add(agentRow{rec: rec, alive: m.agentAlive[rec.ID]})
		}
	}
	return append(active, recent...)
}

// visibleAgents lists agents for the Agents tab, filtered to the hovered epic
// unless show-all is on.
func (m model) visibleAgents() []agentRow {
	epic := m.currentEpic()
	return m.agentRows(func(r agentRow) bool {
		bead := r.bead()
		return m.showAll || epic == "" || bead == epic ||
			(m.graph != nil && m.graph.EpicOf(bead) == epic)
	})
}

// beadAgents lists the agents working one bead, for its detail-page ledger.
func (m model) beadAgents(beadID string) []agentRow {
	return m.agentRows(func(r agentRow) bool { return r.bead() == beadID })
}

func (m model) selectedAgent() (agentRow, bool) {
	agents := m.visibleAgents()
	if m.agentCursor < 0 || m.agentCursor >= len(agents) {
		return agentRow{}, false
	}
	return agents[m.agentCursor], true
}

func (m *model) clampAgentCursor() {
	n := len(m.visibleAgents())
	m.agentCursor = min(max(m.agentCursor, 0), max(n-1, 0))
}

// moveBeadAgent moves the cursor over the current bead's agents ledger, clamped
// to the row count.
func (m *model) moveBeadAgent(d int) {
	n := len(m.beadAgents(m.target()))
	if n == 0 {
		return
	}
	m.beadAgentCursor = min(max(m.beadAgentCursor+d, 0), n-1)
}

func (m *model) clampBeadAgentCursor() {
	n := len(m.beadAgents(m.target()))
	m.beadAgentCursor = min(max(m.beadAgentCursor, 0), max(n-1, 0))
}

// killBeadAgent terminates the focused row of the current bead's agents ledger:
// a beadsboard-owned agent goes through the Manager's dismissal path, an
// external or planning record through the registry (PID signal + record drop).
// Both end with the record gone; the caller refreshes the ledger.
func (m *model) killBeadAgent() {
	rows := m.beadAgents(m.target())
	if m.beadAgentCursor < 0 || m.beadAgentCursor >= len(rows) {
		return
	}
	row := rows[m.beadAgentCursor]
	if row.managed() {
		m.mgr.Dismiss(row.id())
	} else if m.reg != nil {
		_ = m.reg.Kill(row.id())
	}
	m.clampBeadAgentCursor()
}

// hasAgents gates the tab bar. It counts registry records too, so the tab does
// not vanish across a restart while agents are still registered and running.
func (m model) hasAgents() bool {
	return len(m.mgr.Snapshot()) > 0 || len(m.agentRecords) > 0
}

func (m model) anyNeedsInput() bool {
	for _, a := range m.mgr.Snapshot() {
		if a.Status == agent.NeedsInput {
			return true
		}
	}
	return false
}

// --- config live-reload -------------------------------------------------------

func (m *model) reloadConfigIfChanged() {
	fi, err := os.Stat(m.cfgPath)
	if err != nil || fi.ModTime().Equal(m.cfgModTime) {
		return
	}
	if cfg, path, err := config.Load(m.client.Dir); err == nil {
		m.cfg = cfg
		m.cfgPath = path
		m.mgr.SetMaxAgents(cfg.MaxAgents)
	}
	m.cfgModTime = fi.ModTime()
}

// --- settings panel -----------------------------------------------------------

const (
	setMaxAgents = iota
	setMaxTurns
	setPermMode
	setRecentTTL
	setFieldCount
)

var permModes = []string{"acceptEdits", "plan", "bypassPermissions", "default"}

func (m *model) openSettings() {
	m.settingsOpen = true
	m.setField = 0
}

func (m model) handleSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.settingsOpen = false
	case "up", "k":
		m.setField = (m.setField - 1 + setFieldCount) % setFieldCount
	case "down", "j":
		m.setField = (m.setField + 1) % setFieldCount
	case "left", "h":
		m.adjustSetting(-1)
	case "right", "l":
		m.adjustSetting(1)
	case "s", "enter":
		if err := config.Save(m.cfg, m.cfgPath); err != nil {
			m.notice = err.Error()
		}
		m.mgr.SetMaxAgents(m.cfg.MaxAgents)
		if fi, err := os.Stat(m.cfgPath); err == nil {
			m.cfgModTime = fi.ModTime() // absorb our own write
		}
		m.settingsOpen = false
	}
	return m, nil
}

func (m *model) adjustSetting(d int) {
	switch m.setField {
	case setMaxAgents:
		m.cfg.MaxAgents = max(m.cfg.MaxAgents+d, 1)
	case setMaxTurns:
		m.cfg.MaxTurns = max(m.cfg.MaxTurns+d, 0)
	case setPermMode:
		i := max(indexOf(permModes, m.cfg.PermissionMode), 0)
		m.cfg.PermissionMode = permModes[(i+d+len(permModes))%len(permModes)]
	case setRecentTTL:
		m.cfg.RecentTTLSecs = max(m.cfg.RecentTTLSecs+d*30, 30)
	}
}

// --- Agents tab rendering -----------------------------------------------------

func (m model) tabBar(width int) string {
	label := "Agents"
	if n := len(m.mgr.Snapshot()); n > 0 {
		label = fmt.Sprintf("Agents (%d)", n)
		if m.anyNeedsInput() {
			label += " !"
		}
	}
	det, ag := " Details ", " "+label+" "
	if m.tab == tabAgents {
		return dimStyle.Render(det) + selectedStyle.Render(ag)
	}
	return selectedStyle.Render(det) + dimStyle.Render(ag)
}

// agentsColumn stacks the agent list over the selected agent's preview.
func (m model) agentsColumn(rightOuter, innerH int) string {
	topContent, botContent := rightSplit(innerH)
	rightInner := max(rightOuter-4, 1)
	list := boxStyle.Width(rightOuter - 2).Height(topContent).Render(m.agentListContent(rightInner, topContent))
	preview := boxStyle.Width(rightOuter - 2).Height(botContent).Render(m.agentPreviewContent(rightInner, botContent))
	return lipgloss.JoinVertical(lipgloss.Left, list, preview)
}

func (m model) agentListContent(width, height int) string {
	agents := m.visibleAgents()
	scope := "scoped"
	if m.showAll {
		scope = "all"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", dimStyle.Render("AGENTS ("+scope+")"))
	if len(agents) == 0 {
		b.WriteString(dimStyle.Render("none — press a on an epic or task"))
		return b.String()
	}

	activeCount := 0
	for _, a := range agents {
		if a.active() {
			activeCount++
		}
	}

	rows := max(height-2, 1)
	var lines []string
	for i, a := range agents {
		if i == activeCount && activeCount > 0 && activeCount < len(agents) {
			lines = append(lines, dimStyle.Render("· recent ·"))
		}
		lines = append(lines, m.renderAgentRow(a, i == m.agentCursor, width))
	}
	if len(lines) > rows {
		lines = lines[:rows]
	}
	b.WriteString(strings.Join(lines, "\n"))
	return b.String()
}

func (m model) renderAgentRow(r agentRow, selected bool, width int) string {
	if !r.managed() {
		return m.renderRegistryRow(r, selected, width)
	}
	a := *r.view
	summary := a.Summary
	if a.Status == agent.NeedsInput {
		summary = a.Question
	}
	prefix := fmt.Sprintf("%s %-7s %-4s ", agentGlyph(a.Status), shortID(a.IssueID), a.Scope)
	titleW := max(width-lipgloss.Width(prefix), 4)
	line := prefix + truncate(summary, titleW)

	switch {
	case selected:
		return selectedStyle.Width(width).Render(truncate(line, width))
	case a.Status == agent.NeedsInput:
		return lipgloss.NewStyle().Foreground(yellow).Render(truncate(line, width))
	default:
		return truncate(line, width)
	}
}

func (m model) agentPreviewContent(width, height int) string {
	r, ok := m.selectedAgent()
	if !ok {
		return dimStyle.Render("no agent selected")
	}

	if !r.managed() {
		return m.registryPreview(r, width)
	}
	a := *r.view

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", agentGlyph(a.Status),
		dimStyle.Render(a.IssueID+" · "+agentWord(a.Status)+" · "+a.Branch))
	if a.Session != "" {
		b.WriteString(dimStyle.Render("session "+a.Session) + "\n")
	}
	b.WriteByte('\n')

	if a.Status == agent.NeedsInput {
		b.WriteString(lipgloss.NewStyle().Foreground(yellow).Render("NEEDS INPUT") + "\n")
		wrapped := lipgloss.NewStyle().Width(max(width, 1)).Render(a.Question)
		b.WriteString(wrapped)
		return b.String()
	}

	if len(a.Tail) == 0 {
		b.WriteString(dimStyle.Render("… starting"))
		return b.String()
	}
	b.WriteString(strings.Join(tailLines(a.Tail, width, max(height-4, 1), m.wrapLogs), "\n"))
	return b.String()
}

func (m model) settingsView(width, height int) string {
	fields := []struct{ label, val string }{
		{"max agents", strconv.Itoa(m.cfg.MaxAgents)},
		{"max turns", turnsLabel(m.cfg.MaxTurns)},
		{"permission", m.cfg.PermissionMode},
		{"recent ttl", strconv.Itoa(m.cfg.RecentTTLSecs) + "s"},
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", dimStyle.Render("SETTINGS  "+tildePath(m.cfgPath)))
	for i, f := range fields {
		line := fmt.Sprintf("%-12s ‹ %s ›", f.label, f.val)
		if i == m.setField {
			b.WriteString(selectedStyle.Render(" " + line + " "))
		} else {
			b.WriteString(labelStyle.Render("  " + line))
		}
		b.WriteByte('\n')
	}
	b.WriteString("\n" + dimStyle.Render("tools & github sync live in the config file"))
	return b.String()
}

// pickerView draws the launcher matrix — coding/planning rows × claude/codex
// columns — highlighting the armed cell and labelling each with its blind chord.
func (m model) pickerView(width, height int) string {
	modes := []struct{ label, key string }{{"coding", "c"}, {"planning", "p"}}
	tools := []struct{ label, key string }{{"claude", "l"}, {"codex", "o"}}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", dimStyle.Render("LAUNCH "+shortID(m.pickerTarget)+" ("+m.pickerScope+")"))
	for mi, mo := range modes {
		for ti, to := range tools {
			line := fmt.Sprintf("%-9s %-7s a %s %s", mo.label, to.label, mo.key, to.key)
			if mi == m.pickerMode && ti == m.pickerBackend {
				b.WriteString(selectedStyle.Render(" " + line + " "))
			} else {
				b.WriteString(labelStyle.Render("  " + line))
			}
			b.WriteByte('\n')
		}
	}
	b.WriteString("\n" + dimStyle.Render("coding spawns a headless agent · planning opens a local session"))
	return b.String()
}

// tildePath abbreviates the user's home directory to ~ for display.
func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

func turnsLabel(n int) string {
	if n == 0 {
		return "uncapped"
	}
	return strconv.Itoa(n)
}

func agentGlyph(s agent.Status) string {
	switch s {
	case agent.Running:
		return "◐"
	case agent.NeedsInput:
		return "!"
	case agent.Intervened:
		return "⇄"
	case agent.Done:
		return "✓"
	case agent.Failed:
		return "✕"
	case agent.Killed:
		return "∅"
	}
	return "·"
}

func agentWord(s agent.Status) string {
	switch s {
	case agent.Running:
		return "running"
	case agent.NeedsInput:
		return "needs input"
	case agent.Intervened:
		return "intervened"
	case agent.Done:
		return "done"
	case agent.Failed:
		return "failed"
	case agent.Killed:
		return "killed"
	}
	return "unknown"
}

// --- per-bead agents ledger ---------------------------------------------------

// regCmd reads the shared registry off the UI goroutine and computes liveness
// per record, so the render path only ever touches the cached result. The
// registry is created eagerly in New(); guarding nil keeps tests that build the
// model by hand from panicking.
func (m model) regCmd() tea.Cmd {
	reg := m.reg
	if reg == nil {
		return nil
	}
	return func() tea.Msg {
		recs, err := reg.List()
		if err != nil {
			return regLoadedMsg{} // unreadable registry reads as no external agents
		}
		alive := make(map[string]bool, len(recs))
		for _, r := range recs {
			alive[r.ID] = r.Alive()
		}
		return regLoadedMsg{records: recs, alive: alive}
	}
}

// renderRegistryRow draws an agent this board only knows from the registry: its
// bead, who runs it, and whether its process is still there. There is no status
// or summary to show — those come from the Manager, which never saw it.
func (m model) renderRegistryRow(r agentRow, selected bool, width int) string {
	glyph, word := dimStyle.Render("○"), "gone"
	if r.alive {
		glyph, word = lipgloss.NewStyle().Foreground(cyan).Render("●"), "running"
	}
	detail := fmt.Sprintf("%s · %s · %s", word, r.tool(), r.source())
	prefix := fmt.Sprintf("%s %-7s %-4s ", glyph, shortID(r.bead()), r.mode())
	line := prefix + truncate(detail, max(width-lipgloss.Width(prefix), 4))
	if selected {
		plain := fmt.Sprintf("%s %-7s %-4s %s", markFor(r.alive), shortID(r.bead()), r.mode(), detail)
		return selectedStyle.Width(width).Render(truncate(plain, width))
	}
	return line
}

func markFor(alive bool) string {
	if alive {
		return "●"
	}
	return "○"
}

// registryPreview describes an agent this board did not start. There is no log
// to show — the tail lives in the process that runs it — so this reports where it
// is running and how to reach it instead of pretending to be empty.
func (m model) registryPreview(r agentRow, width int) string {
	rec := r.rec
	state := "process gone"
	if r.alive {
		state = "running"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", markFor(r.alive),
		dimStyle.Render(rec.BeadID+" · "+state+" · "+string(rec.Tool)+" · "+string(rec.Mode)))
	if rec.Branch != "" {
		b.WriteString(dimStyle.Render("branch "+rec.Branch) + "\n")
	}
	if rec.SessionID != "" {
		b.WriteString(dimStyle.Render("session "+rec.SessionID) + "\n")
	}
	if rec.Cwd != "" {
		b.WriteString(dimStyle.Render(truncate("cwd "+rec.Cwd, max(width, 1))) + "\n")
	}
	fmt.Fprintf(&b, "%s\n\n", dimStyle.Render(fmt.Sprintf("pid %d · registered by %s", rec.PID, rec.Source)))
	b.WriteString(dimStyle.Render(notManagedHere))
	return b.String()
}

// beadAgentMark is the uncoloured liveness rune, for use inside the selected-row
// highlight (whose background an inner colour reset would otherwise break).
func beadAgentMark(r agentRow) string {
	if r.managed() {
		return agentGlyph(r.view.Status)
	}
	if r.alive {
		return "◐"
	}
	return "·"
}

// beadAgentGlyph is the liveness marker: a managed row reuses the status glyph,
// an external one shows a live/idle dot from its cached liveness.
func beadAgentGlyph(r agentRow) string {
	mark := beadAgentMark(r)
	switch {
	case r.managed():
		return mark
	case r.alive:
		return lipgloss.NewStyle().Foreground(green).Render(mark)
	default:
		return dimStyle.Render(mark)
	}
}

// renderBeadAgents is a compact read-only ledger of every agent working a bead:
// a dim heading then one line per row with a liveness glyph and tool/mode/source
// columns. Clipped to height so it never crowds out the notes above it.
func (m model) renderBeadAgents(rows []agentRow, width, height int) string {
	var b strings.Builder
	b.WriteString(dimStyle.Render("AGENTS"))
	if len(rows) == 0 {
		b.WriteString("\n" + dimStyle.Render("  none"))
		return b.String()
	}
	limit := max(height-1, 1)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	focused := m.taskOpen && m.section == secAgents
	for i, r := range rows {
		id := shortID(r.id())
		cols := fmt.Sprintf("%-7s %-8s %-9s %s", r.tool(), r.mode(), r.source(), r.statusWord())
		if focused && i == m.beadAgentCursor {
			line := fmt.Sprintf("%s %-8s %s", beadAgentMark(r), id, cols)
			b.WriteString("\n" + selectedStyle.Width(width).Render(truncate(line, width)))
			continue
		}
		prefix := fmt.Sprintf("%s %-8s ", beadAgentGlyph(r), id)
		colW := max(width-lipgloss.Width(prefix), 4)
		b.WriteString("\n" + prefix + dimStyle.Render(truncate(cols, colW)))
	}
	return b.String()
}
