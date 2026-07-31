package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A bare "Bash" or "Read" says nothing about what the agent is doing, so the
// argument that identifies the work is pulled out of the tool_use input.
func TestClaudeParseShowsToolArguments(t *testing.T) {
	cases := []struct{ name, line, want string }{
		{
			"bash command",
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`,
			"→ Bash: go test ./...",
		},
		{
			"read path",
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"internal/ui/model.go"}}]}}`,
			"→ Read: internal/ui/model.go",
		},
		{
			"grep pattern",
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Grep","input":{"pattern":"func main"}}]}}`,
			"→ Grep: func main",
		},
		{
			"multi-line command keeps its first line",
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"set -e\nmake build"}}]}}`,
			"→ Bash: set -e",
		},
		{
			"unknown tool degrades to its name",
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"MysteryTool","input":{"whatever":"x"}}]}}`,
			"→ MysteryTool",
		},
		{
			"missing input degrades to its name",
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash"}]}}`,
			"→ Bash",
		},
		{
			"empty argument degrades to its name",
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"  "}}]}}`,
			"→ Read",
		},
	}
	var b claudeBackend
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev, ok := b.Parse([]byte(tc.line))
			require.True(t, ok)
			require.Equal(t, tc.want, ev.Progress)
		})
	}
}

// Text and a tool call in one turn still read as one progress line.
func TestClaudeParseTextWithToolCall(t *testing.T) {
	var b claudeBackend
	ev, ok := b.Parse([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"Checking the tests"},{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`))
	require.True(t, ok)
	require.Equal(t, "Checking the tests  → Bash: go test ./...", ev.Progress)
}
