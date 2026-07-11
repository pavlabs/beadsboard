package beads

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// StatusLabelPrefix marks the label that carries a bead's rich status onto its
// GitHub issue. GitHub issues model only open/closed, so in_progress/blocked
// ride as a status:<value> label that a Projects workflow can map to a board
// column. The prefix is also how the UI recognizes these as sync plumbing
// rather than user labels.
const StatusLabelPrefix = "status:"

// statusLabel is the carrier label for a given status value.
func statusLabel(status string) string { return StatusLabelPrefix + status }

// githubEnv returns the process environment with GITHUB_REPOSITORY set to repo
// when non-empty, so `bd github` targets the configured project rather than
// bd's own default.
func githubEnv(repo string) []string {
	if repo == "" {
		return nil
	}
	return append(os.Environ(), "GITHUB_REPOSITORY="+repo)
}

// Sync pushes a single issue's current state to GitHub via
// `bd github sync --push-only --issues <id>`. Push-only and issue-scoped so a
// status edit never pulls unrelated remote changes into the local DB behind the
// user's back.
func (c *Client) Sync(ctx context.Context, id, repo string) error {
	cmd := exec.CommandContext(ctx, "bd", "github", "sync", "--push-only", "--issues", id)
	cmd.Dir = c.Dir
	cmd.Env = githubEnv(repo)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("bd github sync: %w: %s", err, sanitize(strings.TrimSpace(string(out))))
	}
	return nil
}

// UpdateStatus persists a status change and, in the same bd write, reconciles
// the status:<value> carrier label so exactly one remains: the new status is
// added and every other value in allStatuses is removed (removing an absent
// label is a no-op). Used only when the GitHub sync plugin is enabled; a plain
// Update("status", …) is used otherwise so non-sync repos stay label-free.
func (c *Client) UpdateStatus(ctx context.Context, id, status string, allStatuses []string) error {
	cmd := exec.CommandContext(ctx, "bd", statusUpdateArgs(id, status, allStatuses)...)
	cmd.Dir = c.Dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("bd update status: %w: %s", err, sanitize(strings.TrimSpace(string(out))))
	}
	return nil
}

// statusUpdateArgs builds the `bd update` argv that sets status and leaves
// exactly one status:<value> label: add the new one, remove every other value.
func statusUpdateArgs(id, status string, allStatuses []string) []string {
	args := []string{"update", id, "--status", status, "--add-label", statusLabel(status)}
	for _, s := range allStatuses {
		if s != status {
			args = append(args, "--remove-label", statusLabel(s))
		}
	}
	return args
}
