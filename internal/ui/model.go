package ui

import (
	"context"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavlabs/beadsboard/internal/agent"
	"github.com/pavlabs/beadsboard/internal/beads"
	"github.com/pavlabs/beadsboard/internal/config"
)

const refreshInterval = time.Second

type model struct {
	client *beads.Client
	graph  *beads.Graph
	err    error

	loading bool
	spinner spinner.Model
	detail  viewport.Model // epic fields region (top-right)

	epicCursor int
	wrap       bool // wrap epic titles in the list instead of truncating

	searching   bool            // the search input is capturing keys
	searchScope int             // which list the query filters (scope* below)
	search      textinput.Model // fuzzy search query

	focused    bool // right pane (fields + tasks) has focus
	section    int  // which right-pane section is selected (sec* below)
	taskCursor int  // selected task when the task-list section is focused
	taskOpen   bool // drilled into a task's detail page (reuses field motion)

	editing bool            // inline editing the focused field
	editSec int             // section being edited
	input   textinput.Model // title editor
	area    textarea.Model  // description/notes editor
	choice  int             // status index / priority value while cycling

	cfg        config.Config
	cfgPath    string // resolved config file we watch and save back to
	cfgModTime time.Time
	mgr        *agent.Manager

	tab         int  // tabDetails | tabAgents
	agentCursor int  // selected agent in the Agents tab
	showAll     bool // Agents tab: all agents vs scoped to the hovered epic
	notice      string

	settingsOpen bool
	setField     int // which setting the cursor is on

	fp    uint64
	hasFP bool

	width, height int
}

// Right-pane tabs.
const (
	tabDetails = iota
	tabAgents
)

// Right-pane sections the cursor cycles through with tab.
const (
	secTitle = iota
	secStatus
	secPriority
	secDescription
	secNotes
	secTasks
	sectionCount
)

// taskSectionCount is how many sections a task's detail page cycles through: the
// same fields as an epic minus the task-list section (a task has no subtasks).
const taskSectionCount = secTasks

// Search scopes: which list an active query filters.
const (
	scopeEpics = iota
	scopeTasks
)

// editStatuses are the statuses the status field cycles through when editing.
var editStatuses = []string{"open", "in_progress", "blocked", "closed"}

// Messages.
type (
	hydratedMsg struct {
		graph *beads.Graph
		fp    uint64 // fingerprint measured just after load, used as the new baseline
		err   error
	}
	tickMsg struct{}
	fpMsg   struct {
		fp  uint64
		err error
	}
	editSavedMsg struct {
		err    error
		syncID string // issue to push to GitHub after this save, or "" for none
	}
	agentEventMsg struct{}
	spawnedMsg    struct{ err error }
	interveneMsg  struct{ err error }
	syncedMsg     struct{ err error }
)

// newInputs builds the title and description/notes editors with their shared
// configuration.
func newInputs() (title textinput.Model, body textarea.Model, search textinput.Model) {
	body = textarea.New()
	body.ShowLineNumbers = false
	body.Prompt = ""
	search = textinput.New()
	search.Prompt = ""
	return textinput.New(), body, search
}

// New builds the root model for the given target directory.
func New(dir string) model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	ti, ta, se := newInputs()

	cfg, cfgPath, _ := config.Load(dir)
	mgr := agent.New(dir, "claude", cfg.MaxAgents)
	mgr.Sweep() // clear scratch from any prior crashed run

	var modTime time.Time
	if fi, err := os.Stat(cfgPath); err == nil {
		modTime = fi.ModTime()
	}

	return model{
		client:     beads.NewClient(dir),
		loading:    true,
		spinner:    sp,
		detail:     viewport.New(0, 0),
		input:      ti,
		area:       ta,
		search:     se,
		cfg:        cfg,
		cfgPath:    cfgPath,
		cfgModTime: modTime,
		mgr:        mgr,
	}
}

func (m model) Init() tea.Cmd {
	// The post-load fingerprint from hydrateCmd seeds the watcher baseline, so
	// no independent fpCmd here.
	return tea.Batch(m.spinner.Tick, m.hydrateCmd(), tickCmd(), m.waitAgentEvent())
}

// waitAgentEvent blocks on the manager's event channel and re-arms itself, so
// agent state changes drive re-renders without polling.
func (m model) waitAgentEvent() tea.Cmd {
	ch := m.mgr.Events()
	return func() tea.Msg {
		<-ch
		return agentEventMsg{}
	}
}

func (m model) hydrateCmd() tea.Cmd {
	dir := m.client.Dir
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		issues, err := m.client.Load(ctx)
		if err != nil {
			return hydratedMsg{err: err}
		}
		// `bd export` itself churns Dolt's journal, so snapshot the fingerprint
		// after loading; that becomes the baseline the watcher compares against,
		// leaving only external writes to trigger the next reload.
		fp, _ := beads.Fingerprint(dir)
		return hydratedMsg{graph: beads.BuildGraph(issues), fp: fp}
	}
}

func (m model) fpCmd() tea.Cmd {
	dir := m.client.Dir
	return func() tea.Msg {
		fp, err := beads.Fingerprint(dir)
		return fpMsg{fp: fp, err: err}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

// startReload flips to the loading state and kicks off a fresh hydrate.
func (m model) startReload() (tea.Model, tea.Cmd) {
	m.loading = true
	return m, tea.Batch(m.spinner.Tick, m.hydrateCmd())
}

// updateCmd persists a field edit via `bd update` off the UI goroutine. syncID,
// when set, rides back on the result so the save handler can push that issue to
// GitHub once the local write lands.
func (m model) updateCmd(id, field, value, syncID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return editSavedMsg{err: client.Update(ctx, id, field, value), syncID: syncID}
	}
}

// githubSyncCmd pushes a single issue's current state to GitHub off the UI
// goroutine. It runs concurrently with the reload so a slow API call never
// blocks the interface.
func (m model) githubSyncCmd(id string) tea.Cmd {
	client, repo := m.client, m.cfg.GitHubRepository
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		return syncedMsg{err: client.Sync(ctx, id, repo)}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizeDetail()
		m.syncDetail()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case hydratedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.graph = msg.graph
		m.fp, m.hasFP = msg.fp, true // baseline absorbs our own export's churn
		m.clampCursors()
		m.syncDetail()
		return m, nil

	case tickMsg:
		m.reloadConfigIfChanged()
		m.mgr.PruneRecent(time.Duration(m.cfg.RecentTTLSecs) * time.Second)
		return m, tea.Batch(m.fpCmd(), tickCmd())

	case agentEventMsg:
		m.clampAgentCursor()
		m.resizeDetail() // tab bar appearing/disappearing shifts the right pane
		return m, m.waitAgentEvent()

	case spawnedMsg:
		if msg.err != nil {
			m.notice = msg.err.Error()
		}
		return m, nil

	case interveneMsg:
		if msg.err != nil {
			m.notice = msg.err.Error()
		}
		return m, nil

	case fpMsg:
		// Reload only when an external bd write moved the state away from the
		// baseline captured after our last load.
		if msg.err != nil || !m.hasFP || m.loading {
			return m, nil
		}
		if msg.fp != m.fp {
			return m.startReload()
		}
		return m, nil

	case editSavedMsg:
		if msg.err != nil {
			m.loading = false
			m.err = msg.err
			return m, nil
		}
		reloaded, cmd := m.startReload() // reflect the saved change immediately
		if msg.syncID != "" {
			return reloaded, tea.Batch(cmd, m.githubSyncCmd(msg.syncID))
		}
		return reloaded, cmd

	case syncedMsg:
		if msg.err != nil {
			m.notice = msg.err.Error()
		}
		return m, nil

	case spinner.TickMsg:
		if !m.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.editing {
		return m.handleEditKey(msg)
	}
	if m.settingsOpen {
		return m.handleSettingsKey(msg)
	}
	if m.searching {
		return m.handleSearchKey(msg)
	}
	m.notice = "" // any key dismisses a transient notice
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "r":
		if !m.loading {
			return m.startReload()
		}
		return m, nil
	}
	if m.tab == tabAgents {
		return m.handleAgentsKey(msg)
	}
	if m.taskOpen {
		return m.handleTaskKey(msg)
	}
	if m.focused {
		return m.handleRightKey(msg)
	}
	return m.handleLeftKey(msg)
}

// handleLeftKey drives the epic list.
func (m model) handleLeftKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.moveEpic(-1)
		m.syncDetail()
	case "down", "j":
		m.moveEpic(1)
		m.syncDetail()
	case "esc":
		if m.query() != "" { // clear a confirmed filter
			m.clearSearch()
			m.clampCursors()
			m.syncDetail()
		}
	case "w":
		m.wrap = !m.wrap
	case "/":
		m.startSearch(scopeEpics)
	case "a":
		if id := m.currentEpic(); id != "" {
			m.tab = tabAgents
			return m, m.spawnCmd(id, "epic")
		}
	case "A":
		m.tab = tabAgents
		m.showAll = true
		m.clampAgentCursor()
	case "S":
		m.openSettings()
	case "enter", "l", "right":
		if m.currentEpic() != "" {
			m.clearSearch()
			m.focused = true
			m.section = secTitle
			m.taskCursor = 0
			m.syncDetail()
		}
	}
	return m, nil
}

// handleRightKey drives the fields + task-list sections of the right pane.
func (m model) handleRightKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.searchScope == scopeTasks && m.query() != "" {
			m.clearSearch() // first esc clears the task filter, stay focused
			m.clampCursors()
			m.syncDetail()
			return m, nil
		}
		m.clearSearch()
		m.focused = false
		m.syncDetail()
	case "h", "left":
		m.clearSearch()
		m.focused = false
		m.syncDetail()
	case "tab":
		m.section = (m.section + 1) % sectionCount
		m.syncDetail()
	case "shift+tab":
		m.section = (m.section - 1 + sectionCount) % sectionCount
		m.syncDetail()
	case "e":
		if !m.loading && m.section != secTasks {
			m.beginEdit()
		}
	case "/":
		if m.section == secTasks {
			m.startSearch(scopeTasks)
		}
	case "a":
		if m.section == secTasks {
			if id := m.currentTask(); id != "" {
				m.tab = tabAgents
				return m, m.spawnCmd(id, "task")
			}
		}
	case "enter", "l", "right":
		if m.section == secTasks && m.currentTask() != "" {
			m.openTask()
		}
	case "up", "k":
		if m.section == secTasks {
			m.moveTask(-1)
			m.syncDetail() // refresh the beside-list preview for the new task
		} else {
			m.detail.ScrollUp(1)
		}
	case "down", "j":
		if m.section == secTasks {
			m.moveTask(1)
			m.syncDetail()
		} else {
			m.detail.ScrollDown(1)
		}
	}
	return m, nil
}

// handleTaskKey drives a task's detail page, which reuses the epic's field
// motion (title→status→priority→description→notes) but has no task-list section.
func (m model) handleTaskKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "h", "left":
		m.closeTask()
	case "tab":
		m.section = (m.section + 1) % taskSectionCount
		m.syncDetail()
	case "shift+tab":
		m.section = (m.section - 1 + taskSectionCount) % taskSectionCount
		m.syncDetail()
	case "e":
		if !m.loading {
			m.beginEdit()
		}
	case "a":
		if id := m.target(); id != "" {
			m.tab = tabAgents
			return m, m.spawnCmd(id, "task")
		}
	case "up", "k":
		m.detail.ScrollUp(1)
	case "down", "j":
		m.detail.ScrollDown(1)
	}
	return m, nil
}

// openTask drills into the highlighted task's detail page.
func (m *model) openTask() {
	m.taskOpen = true
	m.section = secTitle
	m.resizeDetail()
	m.syncDetail()
}

// closeTask returns from a task's detail page to the epic's task list.
func (m *model) closeTask() {
	m.taskOpen = false
	m.section = secTasks
	m.resizeDetail()
	m.syncDetail()
}

// startSearch opens an incremental fuzzy filter over the given list scope.
func (m *model) startSearch(scope int) {
	m.searching = true
	m.searchScope = scope
	m.search.SetValue("")
	m.search.Focus()
}

// clearSearch drops any active filter and closes the search input.
func (m *model) clearSearch() {
	m.searching = false
	m.search.SetValue("")
	m.search.Blur()
}

// query is the active filter text.
func (m model) query() string { return m.search.Value() }

// handleSearchKey feeds the incremental search: enter keeps the filter, esc
// clears it, everything else edits the query and re-anchors the cursor to the
// best match.
func (m model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.clearSearch()
		m.clampCursors()
		m.syncDetail()
		return m, nil
	case "enter":
		m.searching = false // keep the filter, stop capturing keys
		m.search.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	if m.searchScope == scopeTasks {
		m.taskCursor = 0
	} else {
		m.epicCursor = 0
	}
	m.syncDetail() // preview follows the top match live
	return m, cmd
}

// fuzzyFilter keeps the ids whose text loosely matches query, ranked best-first
// (tighter, earlier matches win; original order breaks ties).
func fuzzyFilter(ids []string, query string, text func(string) string) []string {
	if query == "" {
		return ids
	}
	type scored struct {
		id    string
		score int
		order int
	}
	var hits []scored
	for i, id := range ids {
		if s, ok := fuzzyScore(text(id), query); ok {
			hits = append(hits, scored{id, s, i})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool {
		if hits[a].score != hits[b].score {
			return hits[a].score < hits[b].score
		}
		return hits[a].order < hits[b].order
	})
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.id
	}
	return out
}

// fuzzyScore matches query as a case-insensitive subsequence of target and
// returns a penalty (lower is better) for gaps and a late first match, or false
// if query is not a subsequence.
func fuzzyScore(target, query string) (int, bool) {
	tr := []rune(strings.ToLower(target))
	qr := []rune(strings.ToLower(query))
	ti, qi, gaps, first := 0, 0, 0, -1
	for ti < len(tr) && qi < len(qr) {
		if tr[ti] == qr[qi] {
			if first < 0 {
				first = ti
			}
			qi++
		} else if qi > 0 {
			gaps++ // only gaps between matched runes count
		}
		ti++
	}
	if qi < len(qr) {
		return 0, false
	}
	return gaps + first, true
}

// beginEdit opens the inline editor for the focused field, primed with its
// current value.
func (m *model) beginEdit() {
	id := m.target()
	if id == "" {
		return
	}
	is := m.graph.Issues[id]
	m.editing = true
	m.editSec = m.section
	switch m.section {
	case secTitle:
		m.input.SetValue(is.Title)
		m.input.CursorEnd()
		m.input.Focus()
	case secStatus:
		m.choice = max(indexOf(editStatuses, is.Status), 0)
	case secPriority:
		m.choice = min(max(is.Priority, 0), 4)
	case secDescription:
		m.area.SetValue(is.Description)
		m.area.Focus()
	case secNotes:
		m.area.SetValue(is.Notes)
		m.area.Focus()
	}
	m.renderFields()
}

// handleEditKey routes keys to the widget backing the field being edited.
func (m model) handleEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.editSec {
	case secStatus, secPriority:
		return m.handleChoiceKey(msg)
	case secTitle:
		return m.handleInputKey(msg)
	default: // description, notes
		return m.handleAreaKey(msg)
	}
}

// handleChoiceKey cycles the status/priority options; enter commits.
func (m model) handleChoiceKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := 5 // priority 0-4
	if m.editSec == secStatus {
		n = len(editStatuses)
	}
	switch msg.String() {
	case "esc":
		m.cancelEdit()
	case "left", "up", "h", "k":
		m.choice = (m.choice - 1 + n) % n
		m.renderFields()
	case "right", "down", "l", "j", "tab":
		m.choice = (m.choice + 1) % n
		m.renderFields()
	case "enter":
		return m.commitEdit()
	}
	return m, nil
}

// handleInputKey edits the title; enter commits, esc cancels.
func (m model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.cancelEdit()
		return m, nil
	case "enter":
		return m.commitEdit()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.renderFields()
	return m, cmd
}

// handleAreaKey edits description/notes; ctrl+s commits, esc cancels, enter is a
// newline.
func (m model) handleAreaKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.cancelEdit()
		return m, nil
	case "ctrl+s":
		return m.commitEdit()
	}
	var cmd tea.Cmd
	m.area, cmd = m.area.Update(msg)
	m.renderFields()
	return m, cmd
}

func (m *model) cancelEdit() {
	m.editing = false
	m.input.Blur()
	m.area.Blur()
	m.renderFields()
}

// commitEdit persists the edited value and reloads.
func (m model) commitEdit() (tea.Model, tea.Cmd) {
	id := m.target()
	field, value := m.editValue()
	m.cancelEdit()
	if id == "" || field == "" {
		return m, nil
	}
	syncID := ""
	if m.shouldSync(field) {
		syncID = id // push this status change to GitHub once the local write lands
	}
	m.loading = true
	return m, tea.Batch(m.spinner.Tick, m.updateCmd(id, field, value, syncID))
}

// shouldSync reports whether persisting field should also push the issue to
// GitHub. Only status changes sync, and only when the feature is enabled.
func (m model) shouldSync(field string) bool {
	return field == "status" && m.cfg.GitHubSync
}

func (m model) editValue() (field, value string) {
	switch m.editSec {
	case secTitle:
		return "title", m.input.Value()
	case secStatus:
		return "status", editStatuses[m.choice]
	case secPriority:
		return "priority", strconv.Itoa(m.choice)
	case secDescription:
		return "description", m.area.Value()
	case secNotes:
		return "notes", m.area.Value()
	}
	return "", ""
}

func indexOf(ss []string, s string) int {
	for i, v := range ss {
		if v == s {
			return i
		}
	}
	return -1
}

func (m *model) moveEpic(d int) {
	n := len(m.visibleEpics())
	if n == 0 {
		return
	}
	m.epicCursor = min(max(m.epicCursor+d, 0), n-1)
}

func (m *model) moveTask(d int) {
	n := len(m.visibleTasks())
	if n == 0 {
		return
	}
	m.taskCursor = min(max(m.taskCursor+d, 0), n-1)
}

func (m *model) clampCursors() {
	if m.graph == nil {
		return
	}
	m.epicCursor = min(max(m.epicCursor, 0), max(len(m.visibleEpics())-1, 0))
	m.taskCursor = min(max(m.taskCursor, 0), max(len(m.visibleTasks())-1, 0))
}

// currentEpic is the epic the cursor is highlighting, or "".
func (m model) currentEpic() string {
	epics := m.visibleEpics()
	if m.epicCursor < 0 || m.epicCursor >= len(epics) {
		return ""
	}
	return epics[m.epicCursor]
}

// currentTask is the task the cursor is highlighting within the current epic, or
// "".
func (m model) currentTask() string {
	tasks := m.visibleTasks()
	if m.taskCursor < 0 || m.taskCursor >= len(tasks) {
		return ""
	}
	return tasks[m.taskCursor]
}

// visibleEpics is the epic list after applying an active epic-scoped filter.
func (m model) visibleEpics() []string {
	if m.graph == nil {
		return nil
	}
	if m.searchScope == scopeEpics && m.query() != "" {
		return fuzzyFilter(m.graph.Epics, m.query(), func(id string) string {
			return m.graph.Issues[id].Title
		})
	}
	return m.graph.Epics
}

// visibleTasks is the current epic's task list after applying an active
// task-scoped filter.
func (m model) visibleTasks() []string {
	tasks := m.currentEpicTasks()
	if m.searchScope == scopeTasks && m.query() != "" {
		return fuzzyFilter(tasks, m.query(), func(id string) string {
			return m.graph.Issues[id].Title
		})
	}
	return tasks
}

// target is the issue that field navigation and editing act on: the drilled-into
// task when a task detail page is open, otherwise the highlighted epic.
func (m model) target() string {
	if m.taskOpen {
		return m.currentTask()
	}
	return m.currentEpic()
}

// currentEpicTasks are the tasks of the highlighted epic, in topo order.
func (m model) currentEpicTasks() []string {
	e := m.currentEpic()
	if e == "" {
		return nil
	}
	return m.graph.Tasks[e]
}
