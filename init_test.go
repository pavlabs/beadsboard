package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestRunSetupCancelledBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	var calls int
	err := runSetup(setupOptions{Dir: dir}, setupDeps{
		In: bytes.NewBufferString("no\n"), Out: &bytes.Buffer{},
		Run:      func(context.Context, string, string, ...string) error { calls++; return nil },
		StartPM:  func(string, config.Config) error { calls++; return nil },
		EnterTUI: func(string) error { calls++; return nil },
	})
	require.ErrorContains(t, err, "cancelled")
	require.Zero(t, calls)
	require.NoFileExists(t, config.LocalPath(dir))
}

func TestRunSetupCancellationDoesNotCreateMissingTarget(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new-project")
	err := runSetup(setupOptions{Dir: dir}, setupDeps{
		In: bytes.NewBufferString("n\n"), Out: &bytes.Buffer{},
		Run:     func(context.Context, string, string, ...string) error { return nil },
		StartPM: func(string, config.Config) error { return nil }, EnterTUI: func(string) error { return nil },
	})
	require.ErrorContains(t, err, "cancelled")
	require.NoDirExists(t, dir)
}

func TestRunSetupBootstrapsSingleRepoPlansThenEntersTUI(t *testing.T) {
	dir := t.TempDir()
	var commands, order []string
	deps := setupDeps{
		In: bytes.NewBuffer(nil), Out: &bytes.Buffer{},
		Run: func(_ context.Context, gotDir, bin string, args ...string) error {
			require.Equal(t, dir, gotDir)
			commands = append(commands, strings.Join(append([]string{bin}, args...), " "))
			return nil
		},
		StartPM: func(root string, cfg config.Config) error {
			order = append(order, "pm")
			require.Equal(t, dir, root)
			require.NotEmpty(t, cfg.PMSession)
			require.Contains(t, pmPrompt(root, cfg), "explicitly confirms")
			return nil
		},
		EnterTUI: func(root string) error { order = append(order, "tui"); require.Equal(t, dir, root); return nil },
	}
	require.NoError(t, runSetup(setupOptions{Dir: dir, Yes: true, Layout: "single", GitHubRepo: "pavlabs/app", EnterTUI: true}, deps))
	require.Equal(t, []string{"git init", "bd init"}, commands)
	require.Equal(t, []string{"pm", "tui"}, order)
	cfg, path, err := config.Load(dir)
	require.NoError(t, err)
	require.Equal(t, config.LocalPath(dir), path)
	require.Equal(t, "single", cfg.ProjectLayout)
	require.True(t, cfg.GitHubSync)
	require.Equal(t, "pavlabs/app", cfg.GitHubRepository)
}

func TestRunSetupMetaRepoDoesNotEnableSingleRepoSync(t *testing.T) {
	dir := t.TempDir()
	deps := setupDeps{In: bytes.NewBuffer(nil), Out: &bytes.Buffer{},
		Run:     func(context.Context, string, string, ...string) error { return nil },
		StartPM: func(string, config.Config) error { return nil }, EnterTUI: func(string) error { return nil }}
	require.NoError(t, runSetup(setupOptions{Dir: dir, Yes: true, Layout: "meta", GitHubRepo: "ignored/repo"}, deps))
	cfg, _, err := config.Load(dir)
	require.NoError(t, err)
	require.Equal(t, "meta", cfg.ProjectLayout)
	require.False(t, cfg.GitHubSync)
}

func TestRunSetupInteractiveExplainsAndSelectsMeta(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("work\n"), 0o644))
	var out bytes.Buffer
	deps := setupDeps{In: bytes.NewBufferString("yes\nmeta\n"), Out: &out,
		Run:     func(context.Context, string, string, ...string) error { return nil },
		StartPM: func(string, config.Config) error { return nil }, EnterTUI: func(string) error { return nil }}
	require.NoError(t, runSetup(setupOptions{Dir: dir}, deps))
	require.Contains(t, out.String(), "existing, unplanned")
	require.Contains(t, out.String(), "single keeps code and Beads together")
	require.Contains(t, out.String(), "meta keeps Beads at the root")
	cfg, _, err := config.Load(dir)
	require.NoError(t, err)
	require.Equal(t, "meta", cfg.ProjectLayout)
}

func TestPMSummarizePersistsWithoutRewritingTOMLByHand(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.PMSession = "session-1"
	require.NoError(t, config.Save(cfg, config.LocalPath(dir)))
	require.NoError(t, runPMCmd([]string{"summarize", "--root", dir, "--summary", "  Three confirmed epics.  "}))
	got, _, err := config.Load(dir)
	require.NoError(t, err)
	require.Equal(t, "Three confirmed epics.", got.PMSummary)
	require.Equal(t, "session-1", got.PMSession)
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
