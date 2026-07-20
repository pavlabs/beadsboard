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
func (m model) spawnCmd(issueID, scope string) tea.Cmd {
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

// interveneCmd opens an interactive resume of the agent's session in a floating
// zellij pane. Requires running inside a zellij session.
func interveneCmd(cwd, session string) tea.Cmd {
	return func() tea.Msg {
		if os.Getenv("ZELLIJ") == "" {
			return interveneMsg{err: fmt.Errorf("not in zellij — resume manually: cd %s && claude --resume %s", cwd, session)}
		}
		name := "resume " + session
		if len(name) > 24 {
			name = name[:24]
		}
		cmd := exec.Command("zellij", "run", "--floating", "--close-on-exit",
			"--name", name, "--cwd", cwd, "--", "claude", "--resume", session)
		if out, err := cmd.CombinedOutput(); err != nil {
			return interveneMsg{err: fmt.Errorf("zellij: %w: %s", err, strings.TrimSpace(string(out)))}
		}
		return interveneMsg{}
	}
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
		if a, ok := m.selectedAgent(); ok && a.Status == agent.Running {
			m.mgr.Kill(a.ID)
		}
	case "x":
		if a, ok := m.selectedAgent(); ok {
			m.mgr.Dismiss(a.ID)
			m.clampAgentCursor()
		}
	case "enter":
		if a, ok := m.selectedAgent(); ok {
			if cwd, sess, ok := m.mgr.Intervene(a.ID); ok {
				return m, interveneCmd(cwd, sess)
			}
			m.notice = "no session captured yet — can't resume"
		}
	}
	return m, nil
}

// visibleAgents lists agents, active first, filtered to the hovered epic unless
// show-all is on.
func (m model) visibleAgents() []agent.View {
	all := m.mgr.Snapshot()
	epic := m.currentEpic()
	var active, recent []agent.View
	for _, a := range all {
		if !m.showAll && epic != "" && a.IssueID != epic &&
			(m.graph == nil || m.graph.EpicOf(a.IssueID) != epic) {
			continue
		}
		if a.Status.Active() {
			active = append(active, a)
		} else {
			recent = append(recent, a)
		}
	}
	return append(active, recent...)
}

func (m model) selectedAgent() (agent.View, bool) {
	agents := m.visibleAgents()
	if m.agentCursor < 0 || m.agentCursor >= len(agents) {
		return agent.View{}, false
	}
	return agents[m.agentCursor], true
}

func (m *model) clampAgentCursor() {
	n := len(m.visibleAgents())
	m.agentCursor = min(max(m.agentCursor, 0), max(n-1, 0))
}

func (m model) hasAgents() bool { return len(m.mgr.Snapshot()) > 0 }

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
		if a.Status.Active() {
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

func (m model) renderAgentRow(a agent.View, selected bool, width int) string {
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
	a, ok := m.selectedAgent()
	if !ok {
		return dimStyle.Render("no agent selected")
	}

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

	tail := a.Tail
	rows := max(height-4, 1)
	if len(tail) > rows {
		tail = tail[len(tail)-rows:]
	}
	if len(tail) == 0 {
		b.WriteString(dimStyle.Render("… starting"))
		return b.String()
	}
	for i, l := range tail {
		b.WriteString(truncate(l, width))
		if i < len(tail)-1 {
			b.WriteByte('\n')
		}
	}
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

// agentRow is one line of the per-bead ledger: an in-process headless agent, an
// external registry record, or an internal agent enriched by its own record.
type agentRow struct {
	id         string
	tool       string
	mode       string
	source     string
	alive      bool
	statusWord string
	internal   bool       // backed by a live in-process agent (glyph/word from view)
	view       agent.View // valid only when internal
}

// beadAgents merges the live in-process agents working beadID with the cached
// registry records for it, deduped by ID (a record matching an internal row
// enriches that row rather than adding a second line), active/alive-first then
// stable — mirroring visibleAgents' ordering.
func (m model) beadAgents(beadID string) []agentRow {
	recByID := make(map[string]agentreg.Record, len(m.agentRecords))
	for _, rec := range m.agentRecords {
		if rec.BeadID == beadID {
			recByID[rec.ID] = rec
		}
	}

	var active, recent []agentRow
	add := func(r agentRow) {
		if r.alive {
			active = append(active, r)
		} else {
			recent = append(recent, r)
		}
	}
	seen := map[string]bool{}
	for _, v := range m.mgr.Snapshot() {
		if v.IssueID != beadID {
			continue
		}
		seen[v.ID] = true
		row := agentRow{
			id: v.ID, tool: "claude", mode: "coding", source: "local",
			alive: v.Status.Active(), statusWord: agentWord(v.Status),
			internal: true, view: v,
		}
		if rec, ok := recByID[v.ID]; ok {
			row.tool, row.mode, row.source = string(rec.Tool), string(rec.Mode), string(rec.Source)
		}
		add(row)
	}
	for _, rec := range m.agentRecords {
		if rec.BeadID != beadID || seen[rec.ID] {
			continue
		}
		alive := m.agentAlive[rec.ID]
		word := "ended"
		if alive {
			word = "running"
		}
		add(agentRow{
			id: rec.ID, tool: string(rec.Tool), mode: string(rec.Mode),
			source: string(rec.Source), alive: alive, statusWord: word,
		})
	}
	return append(active, recent...)
}

// beadAgentGlyph is the liveness marker: an internal row reuses the status glyph,
// an external one shows a live/idle dot from its cached liveness.
func beadAgentGlyph(r agentRow) string {
	if r.internal {
		return agentGlyph(r.view.Status)
	}
	if r.alive {
		return lipgloss.NewStyle().Foreground(green).Render("◐")
	}
	return dimStyle.Render("·")
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
	for _, r := range rows {
		prefix := fmt.Sprintf("%s %-8s ", beadAgentGlyph(r), shortID(r.id))
		cols := fmt.Sprintf("%-7s %-8s %-9s %s", r.tool, r.mode, r.source, r.statusWord)
		colW := max(width-lipgloss.Width(prefix), 4)
		b.WriteString("\n" + prefix + dimStyle.Render(truncate(cols, colW)))
	}
	return b.String()
}
