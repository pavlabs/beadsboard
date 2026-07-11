package beads

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// StatusLabelPrefix marks the label that carries a bead's rich status onto its
// GitHub issue. GitHub issues model only open/closed, so in_progress/blocked
// ride as a status::<value> label that a Projects workflow can map to a board
// column. The key::value form matches bd's own derived labels (type::task,
// priority::critical), and is how the UI recognizes these as sync plumbing
// rather than user labels.
const StatusLabelPrefix = "status::"

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

// Push creates or updates the GitHub issue linked to a bead via
// `bd github push <id>`, setting its external_ref on first push.
func (c *Client) Push(ctx context.Context, id, repo string) error {
	cmd := exec.CommandContext(ctx, "bd", "github", "push", id)
	cmd.Dir = c.Dir
	cmd.Env = githubEnv(repo)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("bd github push: %w: %s", err, sanitize(strings.TrimSpace(string(out))))
	}
	return nil
}

// Pull refreshes a bead from its linked GitHub issue via `bd github pull <id>`.
func (c *Client) Pull(ctx context.Context, id, repo string) error {
	cmd := exec.CommandContext(ctx, "bd", "github", "pull", id)
	cmd.Dir = c.Dir
	cmd.Env = githubEnv(repo)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("bd github pull: %w: %s", err, sanitize(strings.TrimSpace(string(out))))
	}
	return nil
}

// EnsureIssue makes sure a bead has a linked GitHub issue before an agent starts
// work: it pulls the latest for the configured repo, then — if the bead is still
// unlinked (empty external_ref) — pushes to create the issue. Push is idempotent
// on external_ref, so a pull that just linked the bead won't produce a duplicate.
// ref is the bead's external_ref as currently known locally.
func (c *Client) EnsureIssue(ctx context.Context, id, ref, repo string) error {
	if err := c.Pull(ctx, id, repo); err != nil {
		return err
	}
	if ref != "" {
		return nil // already linked
	}
	return c.Push(ctx, id, repo)
}

// GithubNumber parses the issue number from a bead's external_ref, or 0 when the
// ref is empty or not a GitHub link. bd stores the full issue URL
// (https://github.com/owner/repo/issues/42); a bare gh-42 form is also accepted.
func GithubNumber(ref string) int {
	if ref == "" {
		return 0
	}
	tail := ref
	if i := strings.LastIndex(tail, "/"); i >= 0 {
		tail = tail[i+1:] // trailing path segment of the issue URL
	}
	tail = strings.TrimPrefix(tail, "gh-")
	n, err := strconv.Atoi(tail)
	if err != nil {
		return 0
	}
	return n
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
