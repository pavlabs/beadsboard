package agent

import (
	"encoding/json"
	"strconv"
	"strings"
)

// claudeBackend drives Claude Code. bin is the executable path (overridable in
// tests via New/newAt).
type claudeBackend struct{ bin string }

func (c claudeBackend) Bin() string { return c.bin }

// HeadlessArgs runs Claude Code non-interactively with the prompt, streaming
// structured JSON so run() can tail progress and capture the session id.
func (c claudeBackend) HeadlessArgs(spec Spec) []string {
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

// Parse folds one stream-json line into an Event: the session id (carried on the
// init line), an assistant progress note, or the final result text.
func (c claudeBackend) Parse(line []byte) (Event, bool) {
	ev, ok := decode(line)
	if !ok {
		return Event{}, false
	}
	out := Event{Session: sessionID(ev)}
	switch ev["type"] {
	case "assistant":
		out.Progress = assistantText(ev)
	case "result":
		out.Result = resultText(ev)
	}
	return out, true
}

// ResumeArgs resumes a captured session interactively.
func (c claudeBackend) ResumeArgs(session string) []string {
	return []string{"--resume", session}
}

// InteractiveArgs seeds a fresh REPL: Claude Code reads the first positional arg
// as the opening user turn.
func (c claudeBackend) InteractiveArgs(prompt string) []string {
	return []string{prompt}
}

// decode parses one stream-json line into a generic object. Non-JSON lines
// (e.g. stray stderr interleaving) are ignored.
func decode(line []byte) (map[string]any, bool) {
	line = []byte(strings.TrimSpace(string(line)))
	if len(line) == 0 || line[0] != '{' {
		return nil, false
	}
	var ev map[string]any
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, false
	}
	return ev, true
}

func sessionID(ev map[string]any) string {
	s, _ := ev["session_id"].(string)
	return s
}

// assistantText renders an assistant event into a one-line progress note: its
// text and any tool it invoked. It tolerates both the nested message shape and a
// flat text field.
func assistantText(ev map[string]any) string {
	if msg, ok := ev["message"].(map[string]any); ok {
		if content, ok := msg["content"].([]any); ok {
			var parts []string
			for _, c := range content {
				cm, ok := c.(map[string]any)
				if !ok {
					continue
				}
				switch cm["type"] {
				case "text":
					if t, _ := cm["text"].(string); strings.TrimSpace(t) != "" {
						parts = append(parts, firstLine(t))
					}
				case "tool_use":
					if n, _ := cm["name"].(string); n != "" {
						parts = append(parts, "→ "+toolLine(n, cm["input"]))
					}
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, "  ")
			}
		}
	}
	if t, _ := ev["text"].(string); strings.TrimSpace(t) != "" {
		return firstLine(t)
	}
	return ""
}

// toolArg names the field worth showing for each tool: a bare "Bash" or "Read"
// says nothing about what the agent is doing. Anything not listed degrades to
// just its name rather than disappearing.
var toolArg = map[string]string{
	"Bash":         "command",
	"Read":         "file_path",
	"Edit":         "file_path",
	"Write":        "file_path",
	"NotebookEdit": "notebook_path",
	"Glob":         "pattern",
	"Grep":         "pattern",
	"WebFetch":     "url",
	"WebSearch":    "query",
	"Task":         "description",
	"Skill":        "command",
}

// toolLine renders a tool call as "<name>: <what it acted on>". The value is
// kept whole — it is posted to the bead's timeline as well as shown in the log,
// so the pane truncates at render rather than losing it here.
func toolLine(name string, rawInput any) string {
	input, ok := rawInput.(map[string]any)
	if !ok {
		return name
	}
	key, ok := toolArg[name]
	if !ok {
		return name
	}
	v, _ := input[key].(string)
	if v = strings.TrimSpace(firstLine(v)); v == "" {
		return name
	}
	return name + ": " + v
}

func resultText(ev map[string]any) string {
	r, _ := ev["result"].(string)
	return r
}
