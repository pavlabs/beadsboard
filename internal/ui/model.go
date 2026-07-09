package ui

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavlabs/beadsboard/internal/beads"
)

const refreshInterval = time.Second

type focus int

const (
	focusEpics focus = iota
	focusTasks
)

type model struct {
	client *beads.Client
	graph  *beads.Graph
	err    error

	loading bool
	spinner spinner.Model
	detail  viewport.Model

	level      focus
	epicCursor int
	taskCursor int

	editing   bool // field picker is open, awaiting a field choice
	editField int  // index into editFields

	fp    uint64
	hasFP bool

	width, height int
}

// editFields are the bd edit targets the field picker cycles through with tab.
var editFields = []string{"title", "description", "notes"}

const (
	fieldTitle = iota
	fieldDescription
	fieldNotes
)

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
	editFinishedMsg struct{ err error }
)

// New builds the root model for the given target directory.
func New(dir string) model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	return model{
		client:  beads.NewClient(dir),
		loading: true,
		spinner: sp,
		detail:  viewport.New(0, 0),
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

// editCmd hands the terminal to `bd edit <id> --<field>` (opens $EDITOR on the
// chosen field); the board reloads once the editor returns.
func (m model) editCmd(id, field string) tea.Cmd {
	c := exec.Command("bd", "edit", id, "--"+field)
	c.Dir = m.client.Dir
	return tea.ExecProcess(c, func(err error) tea.Msg { return editFinishedMsg{err: err} })
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

	case editFinishedMsg:
		if msg.err != nil {
			m.err = fmt.Errorf("bd edit: %w", msg.err)
			return m, nil
		}
		return m.startReload()

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
	case "e":
		if !m.loading && m.graph != nil && m.currentID() != "" {
			m.editing = true
			m.editField = fieldDescription
		}
	case "up", "k":
		m.moveCursor(-1)
		m.syncDetail()
	case "down", "j":
		m.moveCursor(1)
		m.syncDetail()
	case "enter", "l", "right":
		if m.level == focusEpics && len(m.currentEpicTasks()) > 0 {
			m.level = focusTasks
			m.taskCursor = 0
			m.syncDetail()
		}
	case "esc", "h", "left":
		if m.level == focusTasks {
			m.level = focusEpics
			m.syncDetail()
		}
	case "pgup", "b":
		m.detail.HalfPageUp()
	case "pgdown", "f":
		m.detail.HalfPageDown()
	}
	return m, nil
}

// handleEditKey drives the modal field picker: tab cycles the target field,
// enter launches the editor on it, esc cancels.
func (m model) handleEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.editing = false
	case "tab":
		m.editField = (m.editField + 1) % len(editFields)
	case "shift+tab":
		m.editField = (m.editField - 1 + len(editFields)) % len(editFields)
	case "enter":
		m.editing = false
		if id := m.currentID(); id != "" {
			return m, m.editCmd(id, editFields[m.editField])
		}
	}
	return m, nil
}

func (m *model) moveCursor(d int) {
	n := len(m.currentItems())
	if n == 0 {
		return
	}
	m.setCursor(min(max(m.cursor()+d, 0), n-1))
}

func (m *model) clampCursors() {
	if m.graph == nil {
		return
	}
	if m.epicCursor >= len(m.graph.Epics) {
		m.epicCursor = max(0, len(m.graph.Epics)-1)
	}
	tasks := m.currentEpicTasks()
	if m.taskCursor >= len(tasks) {
		m.taskCursor = max(0, len(tasks)-1)
	}
	if m.level == focusTasks && len(tasks) == 0 {
		m.level = focusEpics
	}
}

// currentItems returns the id list the cursor navigates at the current level.
func (m model) currentItems() []string {
	if m.graph == nil {
		return nil
	}
	if m.level == focusTasks {
		return m.currentEpicTasks()
	}
	return m.graph.Epics
}

func (m model) currentEpicTasks() []string {
	if m.graph == nil || m.epicCursor >= len(m.graph.Epics) {
		return nil
	}
	return m.graph.Tasks[m.graph.Epics[m.epicCursor]]
}

func (m model) cursor() int {
	if m.level == focusTasks {
		return m.taskCursor
	}
	return m.epicCursor
}

func (m *model) setCursor(c int) {
	if m.level == focusTasks {
		m.taskCursor = c
		return
	}
	m.epicCursor = c
}

// currentID is the id the cursor is highlighting, or "".
func (m model) currentID() string {
	items := m.currentItems()
	c := m.cursor()
	if c < 0 || c >= len(items) {
		return ""
	}
	return items[c]
}
