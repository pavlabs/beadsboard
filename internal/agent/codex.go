package agent

import (
	"path/filepath"
	"strings"
)

// codexBackend drives the Codex CLI. `codex exec --json` runs non-interactively
// and streams JSONL events on stdout; the top-level `codex resume`/prompt forms
// cover interactive resume and seeding. bin is the executable path (overridable
// in tests via New/newAt).
type codexBackend struct{ bin string }

func (c codexBackend) Bin() string { return c.bin }

// HeadlessArgs runs Codex non-interactively, streaming JSONL so run() can tail
// progress and capture the session (thread) id. The agent works in cmd.Dir (a
// per-agent worktree), so no -C is needed; workspace-write lets it edit that tree
// without approval prompts (exec mode never prompts). Codex has no
// --max-turns/--allowedTools analogue, so those Claude-specific Spec fields don't
// apply here.
func (c codexBackend) HeadlessArgs(spec Spec) []string {
	return []string{"exec", "--json", "-s", "workspace-write", spec.Prompt}
}

// Parse folds one JSONL event into an Event. thread.started carries the resumable
// session id; Codex conflates narration and the final answer into agent_message
// items (the last one wins as the result), while command and file-change items
// become progress notes.
func (c codexBackend) Parse(line []byte) (Event, bool) {
	ev, ok := decode(line)
	if !ok {
		return Event{}, false
	}
	switch ev["type"] {
	case "thread.started":
		if id, _ := ev["thread_id"].(string); id != "" {
			return Event{Session: id}, true
		}
	case "item.completed":
		item, ok := ev["item"].(map[string]any)
		if !ok {
			return Event{}, false
		}
		if item["type"] == "agent_message" {
			if t, _ := item["text"].(string); strings.TrimSpace(t) != "" {
				return Event{Result: t}, true
			}
			return Event{}, false
		}
		if p := codexItemProgress(item); p != "" {
			return Event{Progress: p}, true
		}
	}
	return Event{}, false
}

// ResumeArgs resumes a captured session interactively; the top-level `resume`
// subcommand opens the TUI on that session id.
func (c codexBackend) ResumeArgs(session string) []string {
	return []string{"resume", session}
}

// InteractiveArgs seeds a fresh interactive session: with no subcommand Codex
// reads the positional prompt as the opening turn.
func (c codexBackend) InteractiveArgs(prompt string) []string {
	return []string{prompt}
}

// codexItemProgress renders a non-message completed item into a one-line progress
// note — the shell command it ran or the files it changed. Item types we don't
// surface return "" and are ignored.
func codexItemProgress(item map[string]any) string {
	switch item["type"] {
	case "command_execution":
		if cmd, _ := item["command"].(string); strings.TrimSpace(cmd) != "" {
			return "→ " + firstLine(cmd)
		}
	case "file_change":
		if changes := codexChanges(item); changes != "" {
			return "→ " + changes
		}
	}
	return ""
}

// codexChanges summarizes a file_change item's edits as "<kind> <file>" entries.
func codexChanges(item map[string]any) string {
	raw, ok := item["changes"].([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, c := range raw {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		path, _ := cm["path"].(string)
		if path == "" {
			continue
		}
		kind, _ := cm["kind"].(string)
		parts = append(parts, strings.TrimSpace(kind+" "+filepath.Base(path)))
	}
	return strings.Join(parts, ", ")
}
