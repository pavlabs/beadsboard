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
// by the issue's full URL (which is what a bead's external_ref stores), so a pull
// can make GitHub authoritative over bead status. The canonical value collapses
// the issue's open/closed state and its status:: carrier label into one bd
// status. Keying by URL rather than number keeps it correct across repos, where
// issue numbers collide.
func (c *Client) IssueStatuses(ctx context.Context, repo string) (map[string]string, error) {
	args := []string{"issue", "list", "--state", "all", "--limit", "1000", "--json", "url,state,labels"}
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
		URL    string `json:"url"`
		State  string `json:"state"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("decode gh issue list: %w", err)
	}

	statuses := make(map[string]string, len(issues))
	for _, is := range issues {
		names := make([]string, len(is.Labels))
		for i, l := range is.Labels {
			names[i] = l.Name
		}
		statuses[is.URL] = issueStatus(is.State, names)
	}
	return statuses, nil
}

// BoardStatuses reads each issue's status from a Projects v2 board's Status
// column via `gh project item-list`, keyed by the issue's full URL, so a
// teammate moving a card flows back into bd. Keying by URL rather than number
// keeps it correct for a board that aggregates issues across sub-repos, where
// numbers collide. Only recognised column names map to a bd status; anything
// else (unset column, custom names) is skipped.
func (c *Client) BoardStatuses(ctx context.Context, owner string, number int) (map[string]string, error) {
	cmd := exec.CommandContext(ctx, "gh", "project", "item-list",
		strconv.Itoa(number), "--owner", owner, "--format", "json", "--limit", "1000")
	cmd.Dir = c.Dir
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			stderr = sanitize(strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh project item-list: %w: %s", err, stderr)
	}

	var res struct {
		Items []struct {
			Status  string `json:"status"`
			Content struct {
				Type string `json:"type"`
				URL  string `json:"url"`
			} `json:"content"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("decode gh project item-list: %w", err)
	}

	statuses := make(map[string]string, len(res.Items))
	for _, it := range res.Items {
		if it.Content.Type != "Issue" {
			continue
		}
		if s := boardStatus(it.Status); s != "" {
			statuses[it.Content.URL] = s
		}
	}
	return statuses, nil
}

// boardStatus maps a Projects Status column name to a bd status, or "" to skip
// (unset column or a name we don't model). Inverse of the forward Action's
// bd-status → column mapping.
func boardStatus(column string) string {
	switch column {
	case "Done":
		return "closed"
	case "In Progress":
		return "in_progress"
	case "Blocked":
		return "blocked"
	case "Todo":
		return "open"
	default:
		return ""
	}
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

// SyncIssues pushes the given beads to GitHub via
// `bd github sync --push-only --issues <ids>`, targeting repo. Push-only and
// issue-scoped so a sync never pulls unrelated remote changes back, and scoped
// to one repo so a meta-repo can push each group to its own repo. bd only sends
// what differs from the last sync, so an unchanged group is a no-op.
func (c *Client) SyncIssues(ctx context.Context, ids []string, repo string) error {
	if len(ids) == 0 {
		return nil
	}
	cmd := exec.CommandContext(ctx, "bd", "github", "sync", "--push-only", "--issues", strings.Join(ids, ","))
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
