package beads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// statusLabelPrefix is bd's carrier for a bead's rich status on the issue
// (status::in_progress, status::blocked, …) — the key::value house style bd's
// push emits. Read here to translate a GitHub-side status back into bd.
const statusLabelPrefix = "status::"

// IssueStatuses reads each issue's status from GitHub via `gh issue list`, keyed
// by issue number, so a pull can make GitHub authoritative over bead status. The
// canonical value collapses the issue's open/closed state and its status::
// carrier label into one bd status.
func (c *Client) IssueStatuses(ctx context.Context, repo string) (map[int]string, error) {
	args := []string{"issue", "list", "--state", "all", "--limit", "1000", "--json", "number,state,labels"}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = c.Dir
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			stderr = sanitize(strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh issue list: %w: %s", err, stderr)
	}

	var issues []struct {
		Number int    `json:"number"`
		State  string `json:"state"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("decode gh issue list: %w", err)
	}

	statuses := make(map[int]string, len(issues))
	for _, is := range issues {
		names := make([]string, len(is.Labels))
		for i, l := range is.Labels {
			names[i] = l.Name
		}
		statuses[is.Number] = issueStatus(is.State, names)
	}
	return statuses, nil
}

// issueStatus collapses a GitHub issue's state and its status:: carrier label
// into a single bd status value: a closed state wins; otherwise the
// status::<value> label if present; otherwise "open".
func issueStatus(state string, labels []string) string {
	if strings.EqualFold(state, "closed") {
		return "closed"
	}
	for _, l := range labels {
		if v, ok := strings.CutPrefix(l, statusLabelPrefix); ok {
			return v
		}
	}
	return "open"
}

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
