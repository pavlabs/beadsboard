package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAllowedToolsWriteAllowsWholeTool(t *testing.T) {
	c := Config{Tools: map[string]string{"git": "write"}}
	require.Equal(t, []string{"Bash(git *)"}, c.AllowedTools())
}

func TestAllowedToolsReadRestrictsKnownTool(t *testing.T) {
	c := Config{Tools: map[string]string{"aws": "read"}}
	got := c.AllowedTools()
	require.Contains(t, got, "Bash(aws * describe*)")
	require.Contains(t, got, "Bash(aws * list*)")
	require.NotContains(t, got, "Bash(aws *)", "read must not open the whole tool")
}

func TestAllowedToolsReadVerblessToolRunsIt(t *testing.T) {
	c := Config{Tools: map[string]string{"jq": "read"}}
	require.Equal(t, []string{"Bash(jq *)"}, c.AllowedTools())
}

func TestAllowedToolsIsStable(t *testing.T) {
	c := Config{Tools: map[string]string{"git": "write", "aws": "read"}}
	require.Equal(t, c.AllowedTools(), c.AllowedTools(), "sorted, deterministic order")
}

func TestDefaultIsSane(t *testing.T) {
	d := Default()
	require.Equal(t, 10, d.MaxAgents)
	require.Equal(t, 0, d.MaxTurns, "uncapped by default")
	require.Equal(t, "acceptEdits", d.PermissionMode)
	require.NotEmpty(t, d.AllowedTools())
}
