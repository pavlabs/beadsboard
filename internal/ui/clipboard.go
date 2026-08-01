package ui

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// clipboardArgv is the platform's "read stdin into the clipboard" command. The
// project ships darwin and linux binaries, and on linux the tool depends on the
// display server, so the first one present wins at call time rather than here.
func clipboardArgv() []string {
	if runtime.GOOS == "darwin" {
		return []string{"pbcopy"}
	}
	for _, c := range [][]string{
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
	} {
		if _, err := exec.LookPath(c[0]); err == nil {
			return c
		}
	}
	return nil
}

// copyCmd puts s on the system clipboard and reports what was copied, so the
// board confirms rather than leaving the user to check by pasting.
func copyCmd(s string) tea.Cmd {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	argv := clipboardArgv()
	return func() tea.Msg {
		if argv == nil {
			return copiedMsg{err: fmt.Errorf("no clipboard tool found (wl-copy, xclip or xsel)")}
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin = strings.NewReader(s)
		if out, err := cmd.CombinedOutput(); err != nil {
			return copiedMsg{err: fmt.Errorf("%s: %w: %s", argv[0], err, strings.TrimSpace(string(out)))}
		}
		return copiedMsg{what: s}
	}
}

// beadRef is a bead id without its project prefix — "e90" for an epic, "e90.4"
// for a task. shortID collapses a task to "#4", which only reads unambiguously
// beside its epic; a copied reference has to stand on its own.
func beadRef(id string) string {
	if _, rest, ok := strings.Cut(id, "-"); ok && rest != "" {
		return rest
	}
	return id
}

// copyText is what c copies: the focused field's own value, or "<ref> <title>"
// when the selection is a bead rather than one of its fields — that pair is what
// you paste into a message or a commit to name a piece of work.
func (m model) copyText() string {
	id := m.target()
	if m.graph == nil || id == "" {
		return ""
	}
	is := m.graph.Issues[id]

	// On the epic list nothing narrower than the bead is selected.
	if !m.focused && !m.taskOpen {
		return beadRef(id) + " " + is.Title
	}
	switch m.section {
	case secTitle:
		return is.Title
	case secStatus:
		return is.Status
	case secPriority:
		return fmt.Sprintf("P%d", is.Priority)
	case secDescription:
		return is.Description
	case secNotes:
		return is.Notes
	}
	// secTasks on an epic (secAgents on a task page shares the slot): the task
	// under the cursor is the selection, not a field of the epic.
	if !m.taskOpen {
		if task := m.currentTask(); task != "" {
			return beadRef(task) + " " + m.graph.Issues[task].Title
		}
	}
	return beadRef(id) + " " + is.Title
}
