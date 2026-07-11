// Package config loads and persists beadsboard's user settings. A per-directory
// ./.beadsboard/config.toml beside the source repo is the source of truth when
// present; otherwise the global ~/.beadsboard/config.toml applies. The chosen
// file is created with defaults on first run and re-read whenever it changes on
// disk, so edits (external or via the in-app settings panel) apply live.
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"
)

// Config is the persisted settings document.
type Config struct {
	MaxAgents      int               `toml:"max_agents"`
	MaxTurns       int               `toml:"max_turns"` // 0 = uncapped
	PermissionMode string            `toml:"permission_mode"`
	RecentTTLSecs  int               `toml:"recent_ttl_secs"` // how long finished agents linger
	Tools          map[string]string `toml:"tools"`           // tool name -> "read" | "write"

	GitHubSync       bool   `toml:"github_sync"`       // push task status changes via `bd github sync`
	GitHubRepository string `toml:"github_repository"` // "owner/repo"; empty = use bd's own github config

	// Projects v2 board for reverse status sync (G reads the Status column so a
	// card move flows back into bd). Number 0 = board sync off; G then falls back
	// to reading issue state + status:: labels instead.
	GitHubProjectOwner  string `toml:"github_project_owner"`
	GitHubProjectNumber int    `toml:"github_project_number"`
}

// Default is the configuration written on first run and used as the base that
// on-disk values are decoded over, so a partial file keeps sane defaults.
func Default() Config {
	return Config{
		MaxAgents:      10,
		MaxTurns:       0,
		PermissionMode: "acceptEdits",
		RecentTTLSecs:  300,
		Tools: map[string]string{
			"bd":     "write",
			"git":    "write",
			"gh":     "write",
			"curl":   "read",
			"jq":     "read",
			"gcloud": "read",
			"aws":    "read",
		},
		GitHubSync:          false,
		GitHubRepository:    "",
		GitHubProjectOwner:  "",
		GitHubProjectNumber: 0,
	}
}

// dirName and fileName make up ".beadsboard/config.toml", used both for the
// global (under $HOME) and the local (under a source repo) config.
const (
	dirName  = ".beadsboard"
	fileName = "config.toml"
)

// globalPath is ~/.beadsboard/config.toml.
func globalPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, dirName, fileName), nil
}

// Resolve returns the config file governing dir: a local ./.beadsboard/
// config.toml beside the source repo when present (the source of truth),
// otherwise the global ~/.beadsboard/config.toml.
func Resolve(dir string) (string, error) {
	local := filepath.Join(dir, dirName, fileName)
	if _, err := os.Stat(local); err == nil {
		return local, nil
	}
	return globalPath()
}

// Load reads the config governing dir and returns it with the resolved path, so
// the caller can watch and save back to the same file. A missing file is
// created with defaults (only reachable for the global fallback, since a local
// path is only chosen when it already exists).
func Load(dir string) (Config, string, error) {
	cfg := Default()
	path, err := Resolve(dir)
	if err != nil {
		return cfg, "", err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, path, Save(cfg, path)
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, path, fmt.Errorf("decode config: %w", err)
	}
	return cfg, path, nil
}

const header = "# beadsboard settings — edits apply live\n\n"

// Save writes the config to path, creating the directory as needed.
func Save(cfg Config, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config dir: %w", err)
	}
	var buf bytes.Buffer
	buf.WriteString(header)
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// AllowedTools translates the tools map into Claude Code --allowedTools
// patterns. "write" allows the whole tool; "read" allows only that tool's
// read-only verbs (best-effort — prefix patterns, not a hard security boundary).
func (c Config) AllowedTools() []string {
	names := make([]string, 0, len(c.Tools))
	for t := range c.Tools {
		names = append(names, t)
	}
	sort.Strings(names)

	var out []string
	for _, t := range names {
		out = append(out, toolPatterns(t, c.Tools[t])...)
	}
	return out
}

// readVerbs lists the read-only command shapes per known subcommand tool. Tools
// absent here are treated as verbless (read == run the tool).
var readVerbs = map[string][]string{
	"aws":    {"* describe*", "* list*", "* get*"},
	"gcloud": {"* list*", "* describe*", "* get-*"},
	"gh":     {"* view*", "* list*", "* status*", "api*"},
	"git":    {"status*", "log*", "diff*", "show*", "branch*", "remote*"},
	"bd":     {"show*", "list*", "ready*", "prime*", "export*"},
}

func toolPatterns(tool, level string) []string {
	if level == "write" {
		return []string{"Bash(" + tool + " *)"}
	}
	verbs, ok := readVerbs[tool]
	if !ok {
		return []string{"Bash(" + tool + " *)"} // verbless tool: read == run
	}
	out := make([]string, len(verbs))
	for i, v := range verbs {
		out[i] = "Bash(" + tool + " " + v + ")"
	}
	return out
}
