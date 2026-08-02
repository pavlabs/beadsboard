package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/pavlabs/beadsboard/internal/config"
)

// runPMCmd gives the planning agent one narrow persistence operation instead of
// asking it to rewrite TOML itself.
func runPMCmd(args []string) error {
	if len(args) == 0 || args[0] != "summarize" {
		return fmt.Errorf("usage: beadsboard pm summarize --root DIR --summary TEXT")
	}
	fs := flag.NewFlagSet("pm summarize", flag.ContinueOnError)
	root := fs.String("root", ".", "beads project root")
	summary := fs.String("summary", "", "compact project planning context")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if strings.TrimSpace(*summary) == "" {
		return fmt.Errorf("--summary is required")
	}
	cfg, path, err := config.Load(*root)
	if err != nil {
		return err
	}
	cfg.PMSummary = strings.TrimSpace(*summary)
	return config.Save(cfg, path)
}
