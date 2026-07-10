package config

import (
	"os"
	"path/filepath"
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
	require.False(t, d.GitHubSync, "github sync off by default")
	require.NotEmpty(t, d.AllowedTools())
}

// A local .beadsboard/config.toml beside the source repo is the source of truth
// and its values (here github sync) load in over the defaults.
func TestResolvePrefersLocalOverGlobal(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, dirName, fileName)
	require.NoError(t, os.MkdirAll(filepath.Dir(local), 0o755))

	cfg := Default()
	cfg.GitHubSync = true
	cfg.GitHubRepository = "acme/widgets"
	require.NoError(t, Save(cfg, local))

	got, path, err := Load(dir)
	require.NoError(t, err)
	require.Equal(t, local, path, "local config wins")
	require.True(t, got.GitHubSync)
	require.Equal(t, "acme/widgets", got.GitHubRepository)
}

// With no local file, resolution falls back to the global ~/.beadsboard path.
func TestResolveFallsBackToGlobal(t *testing.T) {
	dir := t.TempDir()
	path, err := Resolve(dir)
	require.NoError(t, err)
	require.Contains(t, path, filepath.Join(dirName, fileName))
	require.NotContains(t, path, dir, "no local file, so not the source dir")
}
