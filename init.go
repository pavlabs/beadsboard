package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/pavlabs/beadsboard/internal/config"
	"github.com/pavlabs/beadsboard/internal/ui"
)

// editors is the preference order beadsboard init opens the config with.
var editors = []string{"nvim", "vim"}

// runInitCmd parses the init subcommand's flags and runs it.
func runInitCmd(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	source := fs.String("source", ".", "directory to initialize")
	yes := fs.Bool("yes", false, "confirm initialization non-interactively")
	layout := fs.String("layout", "", "single or meta (prompted when omitted)")
	repo := fs.String("github-repo", "", "GitHub owner/repo for a single-repo project")
	noTUI := fs.Bool("no-tui", false, "bootstrap and plan without opening the board")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return runSetup(setupOptions{Dir: *source, Yes: *yes, Layout: *layout, GitHubRepo: *repo, EnterTUI: !*noTUI}, setupDeps{
		In: os.Stdin, Out: os.Stdout, Run: runSetupCommand, StartPM: startSetupPM, EnterTUI: enterSetupTUI,
	})
}

type setupOptions struct {
	Dir, Layout, GitHubRepo string
	Yes, EnterTUI           bool
}

type setupDeps struct {
	In       io.Reader
	Out      io.Writer
	Run      func(context.Context, string, string, ...string) error
	StartPM  func(string, config.Config) error
	EnterTUI func(string) error
}

// runSetup is the testable bootstrap workflow. Nothing mutates the target until
// confirmation, and existing .beads projects are never reinitialized.
func runSetup(opts setupOptions, deps setupDeps) error {
	abs, err := filepath.Abs(opts.Dir)
	if err != nil {
		return err
	}
	fi, statErr := os.Stat(abs)
	if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	if statErr == nil && !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", abs)
	}
	if beadInfo, err := os.Stat(filepath.Join(abs, ".beads")); err == nil && beadInfo.IsDir() {
		return fmt.Errorf("%s is already a beads project; open it with beadsboard --source %s", abs, abs)
	}
	var entries []os.DirEntry
	if statErr == nil {
		entries, err = os.ReadDir(abs)
		if err != nil {
			return err
		}
	}
	reader := bufio.NewReader(deps.In)
	if !opts.Yes {
		kind := "empty"
		if len(entries) > 0 {
			kind = "existing, unplanned"
		}
		if _, err := fmt.Fprintf(deps.Out, "%s directory: %s\nInitialize git, Beads, and a project manager? [y/N] ", kind, abs); err != nil {
			return err
		}
		answer, _ := reader.ReadString('\n')
		if !strings.EqualFold(strings.TrimSpace(answer), "y") && !strings.EqualFold(strings.TrimSpace(answer), "yes") {
			return fmt.Errorf("initialization cancelled")
		}
	}
	layout := strings.ToLower(strings.TrimSpace(opts.Layout))
	if layout == "" && opts.Yes {
		layout = "single"
	}
	if layout == "" {
		if _, err := fmt.Fprintln(deps.Out, "Repository layout:"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(deps.Out, "  single keeps code and Beads together"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(deps.Out, "  meta keeps Beads at the root and code in linked sub-repositories"); err != nil {
			return err
		}
		if _, err := fmt.Fprint(deps.Out, "Choose single or meta [single]: "); err != nil {
			return err
		}
		answer, _ := reader.ReadString('\n')
		layout = strings.ToLower(strings.TrimSpace(answer))
		if layout == "" {
			layout = "single"
		}
	}
	if layout != "single" && layout != "meta" {
		return fmt.Errorf("--layout must be single or meta")
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(abs, ".git")); os.IsNotExist(err) {
		if err := deps.Run(context.Background(), abs, "git", "init"); err != nil {
			return fmt.Errorf("git init: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect git repository: %w", err)
	}
	if err := deps.Run(context.Background(), abs, "bd", "init"); err != nil {
		return fmt.Errorf("bd init: %w", err)
	}
	cfg := config.Default()
	cfg.ProjectLayout = layout
	cfg.GitHubRepository = strings.TrimSpace(opts.GitHubRepo)
	cfg.GitHubSync = layout == "single" && cfg.GitHubRepository != ""
	cfg.PMSession = newPMSession()
	cfg.PMSummary = "Project manager initialized. Recover current plans with bd prime before proposing changes."
	if err := config.Save(cfg, config.LocalPath(abs)); err != nil {
		return err
	}
	if err := deps.StartPM(abs, cfg); err != nil {
		return err
	}
	if opts.EnterTUI {
		return deps.EnterTUI(abs)
	}
	return nil
}

func newPMSession() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("pm-%d", os.Getpid())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	raw := hex.EncodeToString(b)
	return raw[:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:]
}

func runSetupCommand(ctx context.Context, dir, bin string, args ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func pmPrompt(root string, cfg config.Config) string {
	return fmt.Sprintf("You are this project's persistent product manager. Start by running `bd -C %s prime`. Recovery summary: %s\n\nInterview the user about outcomes, constraints, and what not to build. Propose the smallest useful set of epics. Do not create or update any bead until the user explicitly confirms the proposal; after confirmation use `bd -C %s create` and dependencies to record the lean plan. Before ending, persist a compact recovery summary with `beadsboard pm summarize --root %s --summary <text>`.", root, cfg.PMSummary, root, root)
}

func startSetupPM(root string, cfg config.Config) error {
	if os.Getenv("ZELLIJ") == "" {
		return fmt.Errorf("setup needs Zellij for the project-manager interview; start a Zellij session and rerun beadsboard init")
	}
	args := []string{"run", "--floating", "--name", "project manager", "--cwd", root, "--", "claude", "--session-id", cfg.PMSession, pmPrompt(root, cfg)}
	cmd := exec.Command("zellij", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

var enterSetupTUI = func(root string) error {
	p := tea.NewProgram(ui.New(root), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// runInit creates dir's local .beadsboard/config.toml with defaults (leaving an
// existing one untouched) and opens it in an editor. It's the `beadsboard init`
// subcommand — scoped to the beadsboard config, nothing to do with .beads.
func runInit(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	path := config.LocalPath(abs)

	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := config.Save(config.Default(), path); err != nil {
			return err
		}
		fmt.Println("beadsboard: created", path)
	} else if err != nil {
		return err
	}

	editor, err := pickEditor(exec.LookPath)
	if err != nil {
		return err
	}
	return openEditor(editor, path)
}

// pickEditor returns the first of editors found on PATH via look, or an error
// naming what it searched for.
func pickEditor(look func(string) (string, error)) (string, error) {
	for _, e := range editors {
		if _, err := look(e); err == nil {
			return e, nil
		}
	}
	return "", fmt.Errorf("no editor found on PATH (looked for %v)", editors)
}

// openEditor runs editor on path with the terminal handed through, so the user
// edits the config in place.
func openEditor(editor, path string) error {
	cmd := exec.Command(editor, path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
