package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/pavlabs/beadsboard/internal/config"
)

// editors is the preference order beadsboard init opens the config with.
var editors = []string{"nvim", "vim"}

// runInitCmd parses the init subcommand's flags and runs it.
func runInitCmd(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	source := fs.String("source", ".", "directory to create the .beadsboard config in")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return runInit(*source)
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
