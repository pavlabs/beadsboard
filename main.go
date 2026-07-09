package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavlabs/beadsboard/internal/ui"
)

func main() {
	source := flag.String("source", ".", "beads repository directory to browse (must contain .beads/)")
	flag.Parse()

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
