package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pavlabs/beadsboard/internal/config"
)

// pickEditor prefers nvim, falls back to vim, and errors when neither is on PATH.
func TestPickEditor(t *testing.T) {
	found := func(want string) func(string) (string, error) {
		return func(name string) (string, error) {
			if name == want {
				return "/usr/bin/" + name, nil
			}
			return "", os.ErrNotExist
		}
	}

	e, err := pickEditor(func(string) (string, error) { return "/usr/bin/x", nil })
	require.NoError(t, err)
	require.Equal(t, "nvim", e, "nvim wins when both resolve")

	e, err = pickEditor(found("vim"))
	require.NoError(t, err)
	require.Equal(t, "vim", e, "falls back to vim")

	_, err = pickEditor(func(string) (string, error) { return "", os.ErrNotExist })
	require.Error(t, err, "neither on PATH")
}

// runInit writes a default config where none exists and leaves an existing one
// untouched. Editor launch is exercised via a stub on PATH.
func TestRunInitCreatesThenPreserves(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", stubEditor(t))

	require.NoError(t, runInit(dir))
	path := config.LocalPath(dir)
	require.FileExists(t, path)

	// A hand-edited value must survive a second init (no clobber).
	require.NoError(t, os.WriteFile(path, []byte("# beadsboard\nmax_agents = 3\n"), 0o644))
	require.NoError(t, runInit(dir))
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(body), "max_agents = 3")
}

// stubEditor puts a no-op `nvim` on a fresh PATH dir and returns that dir.
func stubEditor(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "nvim"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	return bin
}
