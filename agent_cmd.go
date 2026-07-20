package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pavlabs/beadsboard/internal/agentreg"
)

// runAgentCmd handles `beadsboard agent register|unregister` — the CLI the
// SessionStart/SessionEnd hook and the planning launcher use to record an agent
// in a project's .beadsboard/agents registry.
func runAgentCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: beadsboard agent register|unregister [flags]")
	}
	switch args[0] {
	case "register":
		return runAgentRegister(args[1:])
	case "unregister":
		return runAgentUnregister(args[1:])
	default:
		return fmt.Errorf("unknown agent subcommand: %q", args[0])
	}
}

func runAgentRegister(args []string) error {
	fs := flag.NewFlagSet("agent register", flag.ExitOnError)
	id := fs.String("id", "", "unique agent/session id (required)")
	bead := fs.String("bead", "", "bead id the agent works (required)")
	mode := fs.String("mode", "coding", "coding|planning")
	source := fs.String("source", "external", "external|beadsboard")
	tool := fs.String("tool", "claude", "claude|codex")
	session := fs.String("session", "", "resumable session id")
	cwd := fs.String("cwd", "", "working directory (default: PWD)")
	pid := fs.Int("pid", 0, "process id for liveness")
	root := fs.String("root", "", "beads root (default: walk up from cwd for .beads)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" || *bead == "" {
		return fmt.Errorf("--id and --bead are required")
	}
	dir := dirOrCwd(*cwd)
	beadsRoot, err := resolveRoot(*root, dir)
	if err != nil {
		return err
	}
	return agentreg.New(beadsRoot).Put(agentreg.Record{
		ID: *id, BeadID: *bead,
		Tool: agentreg.Tool(*tool), Mode: agentreg.Mode(*mode),
		Source: agentreg.Source(*source), SessionID: *session,
		Cwd: dir, PID: *pid, StartedAt: time.Now(),
	})
}

func runAgentUnregister(args []string) error {
	fs := flag.NewFlagSet("agent unregister", flag.ExitOnError)
	id := fs.String("id", "", "agent id (required)")
	cwd := fs.String("cwd", "", "working directory (default: PWD)")
	root := fs.String("root", "", "beads root (default: walk up from cwd for .beads)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	beadsRoot, err := resolveRoot(*root, dirOrCwd(*cwd))
	if err != nil {
		return err
	}
	return agentreg.New(beadsRoot).Remove(*id)
}

func dirOrCwd(cwd string) string {
	if cwd != "" {
		return cwd
	}
	d, _ := os.Getwd()
	return d
}

func resolveRoot(root, dir string) (string, error) {
	if root != "" {
		return root, nil
	}
	return findBeadsRoot(dir)
}

// findBeadsRoot walks up from dir to the nearest ancestor holding a .beads
// directory — the beads project root, where the registry lives. In a meta-repo
// this resolves a sub-repo cwd back to the root that owns the beads.
func findBeadsRoot(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if fi, err := os.Stat(filepath.Join(dir, ".beads")); err == nil && fi.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no .beads project found from %s upward", dir)
		}
		dir = parent
	}
}
