package agent

import "github.com/pavlabs/beadsboard/internal/agentreg"

// Event is the normalized result of parsing one line of a backend's output. At
// most one of Progress/Result is set; Session carries the resumable session id
// when the line announces it.
type Event struct {
	Session  string
	Progress string
	Result   string
}

// Backend abstracts a coding-agent CLI (Claude Code, Codex, …): how to invoke it
// headless, how to read its streamed output, and how to resume or seed an
// interactive session. Its methods let the Manager stay tool-agnostic.
type Backend interface {
	Bin() string                            // executable (path overridable in tests)
	HeadlessArgs(spec Spec) []string        // args for a headless, streamed run
	Parse(line []byte) (Event, bool)        // parse one output line; false = ignore it
	ResumeArgs(session string) []string     // interactive resume of a captured session
	InteractiveArgs(prompt string) []string // fresh interactive session seeded with a prompt
}

var (
	_ Backend = claudeBackend{}
	_ Backend = codexBackend{}
)

// backendFor resolves the backend for tool, defaulting to claude for the empty
// or unknown tool.
func (m *Manager) backendFor(tool agentreg.Tool) Backend {
	if b, ok := m.backends[string(tool)]; ok {
		return b
	}
	return m.backends[string(agentreg.ToolClaude)]
}

// Backend returns the backend for tool (claude fallback), so the UI can resume or
// seed an interactive session with the right CLI.
func (m *Manager) Backend(tool agentreg.Tool) Backend { return m.backendFor(tool) }
