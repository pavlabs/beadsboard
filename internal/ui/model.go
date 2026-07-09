package ui

import (
	"context"
	"strconv"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavlabs/beadsboard/internal/beads"
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

	focused    bool // right pane (fields + tasks) has focus
	section    int  // which right-pane section is selected (sec* below)
	taskCursor int  // selected task when the task-list section is focused

	editing bool            // inline editing the focused field
	editSec int             // section being edited
	input   textinput.Model // title editor
	area    textarea.Model  // description/notes editor
	choice  int             // status index / priority value while cycling

	fp    uint64
	hasFP bool

	width, height int
}

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
	editSavedMsg struct{ err error }
)

// newInputs builds the title and description/notes editors with their shared
// configuration.
func newInputs() (textinput.Model, textarea.Model) {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	return textinput.New(), ta
}

// New builds the root model for the given target directory.
func New(dir string) model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	ti, ta := newInputs()

	return model{
		client:  beads.NewClient(dir),
		loading: true,
		spinner: sp,
		detail:  viewport.New(0, 0),
		input:   ti,
		area:    ta,
	}
}

func (m model) Init() tea.Cmd {
	// The post-load fingerprint from hydrateCmd seeds the watcher baseline, so
	// no independent fpCmd here.
	return tea.Batch(m.spinner.Tick, m.hydrateCmd(), tickCmd())
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

// updateCmd persists a field edit via `bd update` off the UI goroutine.
func (m model) updateCmd(id, field, value string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return editSavedMsg{err: client.Update(ctx, id, field, value)}
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
		return m, tea.Batch(m.fpCmd(), tickCmd())

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
		return m.startReload() // reflect the saved change immediately

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
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "r":
		if !m.loading {
			return m.startReload()
		}
		return m, nil
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
	case "enter", "l", "right":
		if m.currentEpic() != "" {
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
	case "esc", "h", "left":
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
	case "up", "k":
		if m.section == secTasks {
			m.moveTask(-1)
		} else {
			m.detail.ScrollUp(1)
		}
	case "down", "j":
		if m.section == secTasks {
			m.moveTask(1)
		} else {
			m.detail.ScrollDown(1)
		}
	}
	return m, nil
}

// beginEdit opens the inline editor for the focused field, primed with its
// current value.
func (m *model) beginEdit() {
	id := m.currentEpic()
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
	id := m.currentEpic()
	field, value := m.editValue()
	m.cancelEdit()
	if id == "" || field == "" {
		return m, nil
	}
	m.loading = true
	return m, tea.Batch(m.spinner.Tick, m.updateCmd(id, field, value))
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
	if m.graph == nil || len(m.graph.Epics) == 0 {
		return
	}
	m.epicCursor = min(max(m.epicCursor+d, 0), len(m.graph.Epics)-1)
}

func (m *model) moveTask(d int) {
	n := len(m.currentEpicTasks())
	if n == 0 {
		return
	}
	m.taskCursor = min(max(m.taskCursor+d, 0), n-1)
}

func (m *model) clampCursors() {
	if m.graph == nil {
		return
	}
	m.epicCursor = min(max(m.epicCursor, 0), max(len(m.graph.Epics)-1, 0))
	m.taskCursor = min(max(m.taskCursor, 0), max(len(m.currentEpicTasks())-1, 0))
}

// currentEpic is the epic the cursor is highlighting, or "".
func (m model) currentEpic() string {
	if m.graph == nil || m.epicCursor < 0 || m.epicCursor >= len(m.graph.Epics) {
		return ""
	}
	return m.graph.Epics[m.epicCursor]
}

// currentEpicTasks are the tasks of the highlighted epic, in topo order.
func (m model) currentEpicTasks() []string {
	e := m.currentEpic()
	if e == "" {
		return nil
	}
	return m.graph.Tasks[e]
}
