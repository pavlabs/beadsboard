package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOllamaArgsAndParse(t *testing.T) {
	t.Setenv("OLLAMA_MODEL", "deepseek-r1:7b")
	b := ollamaBackend{bin: "ollama"}

	require.Equal(t, []string{"run", "deepseek-r1:7b", "plan this"}, b.InteractiveArgs("plan this"))
	require.Equal(t, []string{"run", "deepseek-r1:7b", "plan this"}, b.HeadlessArgs(Spec{Prompt: "plan this"}))
	require.Nil(t, b.ResumeArgs("ignored"), "plain ollama has no resumable session")
	require.Equal(t, Event{Result: "a plan"}, mustParseOllama(t, b, []byte("  a plan  \n")))
	_, ok := b.Parse([]byte("  \n"))
	require.False(t, ok)
}

func TestOllamaDefaultModel(t *testing.T) {
	t.Setenv("OLLAMA_MODEL", "")
	require.Equal(t, "qwen3:8b", ollamaModel())
}

func mustParseOllama(t *testing.T, b ollamaBackend, line []byte) Event {
	t.Helper()
	ev, ok := b.Parse(line)
	require.True(t, ok)
	return ev
}
