package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavlabs/beadsboard/internal/ui"
)

// version is stamped via -ldflags at release build time; otherwise it falls
// back to the module build info so `go install ...@vX` reports the right tag.
var version = ""

func main() {
	if len(os.Args) > 1 && os.Args[1] == "init" {
		if err := runInitCmd(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "beadsboard:", err)
			os.Exit(1)
		}
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "agent" {
		if err := runAgentCmd(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "beadsboard:", err)
			os.Exit(1)
		}
		return
	}

	source := flag.String("source", ".", "beads repository directory to browse (must contain .beads/)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("beadsboard", resolveVersion())
		return
	}

	abs, err := filepath.Abs(*source)
	if err != nil {
		fmt.Fprintln(os.Stderr, "beadsboard:", err)
		os.Exit(1)
	}
	if _, err := os.Stat(filepath.Join(abs, ".beads")); err != nil {
		fmt.Fprintf(os.Stderr, "beadsboard: no .beads directory in %s\n", abs)
		os.Exit(1)
	}

	p := tea.NewProgram(ui.New(abs), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "beadsboard:", err)
		os.Exit(1)
	}
}

func resolveVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "dev"
}
