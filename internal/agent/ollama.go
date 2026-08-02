package agent

import (
	"os"
	"strings"
)

// ollamaBackend is deliberately planning-only. The plain Ollama CLI can run a
// local model, but it has no autonomous tool loop, structured event stream, or
// resumable session, so the launcher refuses to use it for coding agents.
type ollamaBackend struct{ bin string }

func (o ollamaBackend) Bin() string { return o.bin }

func (o ollamaBackend) HeadlessArgs(spec Spec) []string {
	return []string{"run", ollamaModel(), spec.Prompt}
}

// Parse treats a non-empty output line as model text. Headless Ollama is not
// exposed by the launcher, but keeping the Backend implementation honest makes
// direct callers and future harness work predictable.
func (o ollamaBackend) Parse(line []byte) (Event, bool) {
	text := strings.TrimSpace(string(line))
	if text == "" {
		return Event{}, false
	}
	return Event{Result: text}, true
}

// Plain Ollama has no session identifier or resume operation.
func (o ollamaBackend) ResumeArgs(string) []string { return nil }

func (o ollamaBackend) InteractiveArgs(prompt string) []string {
	return []string{"run", ollamaModel(), prompt}
}

func ollamaModel() string {
	if model := strings.TrimSpace(os.Getenv("OLLAMA_MODEL")); model != "" {
		return model
	}
	return "qwen3:8b"
}
