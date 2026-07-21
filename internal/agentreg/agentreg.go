// Package agentreg is a shared, on-disk registry of agents working on beads,
// living under a project's .beadsboard/agents/ directory. Both beadsboard-spawned
// agents and external sessions (registered via a hook) drop one JSON record per
// agent, so the UI can show every agent attached to a bead regardless of who
// launched it. Each record is the whole file <id>.json; its owner rewrites it to
// update and deletes it to deregister.
package agentreg

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/pavlabs/beadsboard/internal/config"
)

// Tool is the agent backend; Mode is what it's doing; Source is who registered it.
type (
	Tool   string
	Mode   string
	Source string
)

const (
	ToolClaude Tool = "claude"
	ToolCodex  Tool = "codex"

	ModeCoding   Mode = "coding"
	ModePlanning Mode = "planning"

	SourceBeadsboard Source = "beadsboard"
	SourceExternal   Source = "external"
)

// Record is one agent's registry entry.
type Record struct {
	ID        string    `json:"id"`
	BeadID    string    `json:"bead_id"`
	Tool      Tool      `json:"tool"`
	Mode      Mode      `json:"mode"`
	PID       int       `json:"pid"`
	SessionID string    `json:"session_id,omitempty"`
	Cwd       string    `json:"cwd,omitempty"`
	Branch    string    `json:"branch,omitempty"`
	Source    Source    `json:"source"`
	StartedAt time.Time `json:"started_at"`
}

// Alive reports whether the recorded process still exists. Signal 0 runs the
// kernel's existence/permission check without delivering a signal. PID reuse can
// yield a false positive — this is a liveness hint, not a guarantee.
func (r Record) Alive() bool {
	if r.PID <= 0 {
		return false
	}
	err := syscall.Kill(r.PID, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Registry is the agents directory for one beads project.
type Registry struct {
	dir string
}

// New returns the registry under beadsRoot/.beadsboard/agents. It touches no
// disk: the directory is created lazily on the first Put, so merely browsing a
// repo (no agents launched) never litters it with a .beadsboard/agents dir.
func New(beadsRoot string) *Registry {
	return &Registry{dir: config.AgentsDir(beadsRoot)}
}

// Dir is the registry directory, exposed for hooks and tests.
func (r *Registry) Dir() string { return r.dir }

// Put writes rec, replacing any existing record with the same ID. The write is
// atomic (temp + rename) so a concurrent List never observes a half-written file.
func (r *Registry) Put(rec Record) error {
	if rec.ID == "" || strings.ContainsAny(rec.ID, `/\`) {
		return fmt.Errorf("invalid record id: %q", rec.ID)
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return fmt.Errorf("agents dir: %w", err)
	}
	path := r.path(rec.ID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Remove deregisters id. A missing record is not an error (idempotent).
func (r *Registry) Remove(id string) error {
	if err := os.Remove(r.path(id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// List returns every record, oldest first. Files that race with a concurrent
// removal or are malformed are skipped rather than failing the whole read.
func (r *Registry) List() ([]Record, error) {
	entries, err := os.ReadDir(r.dir)
	if os.IsNotExist(err) {
		return nil, nil // lazy: never written, so no agents
	}
	if err != nil {
		return nil, err
	}
	var recs []Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(r.dir, e.Name()))
		if err != nil {
			continue
		}
		var rec Record
		if json.Unmarshal(b, &rec) != nil {
			continue
		}
		recs = append(recs, rec)
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].StartedAt.Before(recs[j].StartedAt) })
	return recs, nil
}

// ForBead returns the records attached to beadID.
func (r *Registry) ForBead(beadID string) ([]Record, error) {
	all, err := r.List()
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, rec := range all {
		if rec.BeadID == beadID {
			out = append(out, rec)
		}
	}
	return out, nil
}

// Reap removes records whose process is no longer alive and returns how many it
// dropped — cleaning up after sessions (usually external) that died without
// deregistering. It replaces a blind sweep, which would also delete live agents.
func (r *Registry) Reap() (int, error) {
	all, err := r.List()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, rec := range all {
		if !rec.Alive() {
			if r.Remove(rec.ID) == nil {
				n++
			}
		}
	}
	return n, nil
}

func (r *Registry) path(id string) string { return filepath.Join(r.dir, id+".json") }
