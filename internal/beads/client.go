package beads

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// maxExport caps how much `bd export` output we read into memory, so a
// pathological repository cannot exhaust the process.
const maxExport = 64 << 20 // 64 MiB

// Client shells out to the `bd` CLI. Commands run with Dir as the working
// directory because bd keys its embedded state to the repository directory.
type Client struct {
	Dir string

	// originRepos memoizes sub-repo remote lookups. Resolving a bead's repo
	// shells out to `git remote get-url`, and a meta-repo asks the same handful
	// of questions once per bead — 150 beads over 15 sub-repos is 150
	// subprocesses to learn 15 facts. Remotes don't move during a session.
	mu          sync.Mutex
	originRepos map[string]string
}

func NewClient(dir string) *Client {
	return &Client{Dir: dir, originRepos: map[string]string{}}
}

// Load returns every issue, fully hydrated, via a single `bd export --all`,
// plus a revision hash of the exported data. The hash is what the board watches
// for change: `bd` rewrites Dolt's journal and .beads/last-touched on reads as
// well as writes, so file state cannot tell our own polling apart from someone
// else's edit, whereas the data only moves when the issues actually do.
// Each `bd` invocation cold-starts an embedded Dolt engine (~0.3s) and
// concurrent invocations contend, so one bulk export beats per-issue fetches.
// Untrusted text fields are sanitized here so no downstream consumer has to.
func (c *Client) Load(ctx context.Context) (map[string]Issue, uint64, error) {
	cmd := exec.CommandContext(ctx, "bd", "export", "--all")
	cmd.Dir = c.Dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, 0, fmt.Errorf("bd export: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, 0, fmt.Errorf("bd export: %w", err)
	}

	byID, rev, decodeErr := decodeExport(stdout)
	// Drain any remainder so the child never blocks on a full pipe, then reap it.
	_, _ = io.Copy(io.Discard, stdout)
	waitErr := cmd.Wait()

	switch {
	case waitErr != nil:
		if s := sanitize(strings.TrimSpace(stderr.String())); s != "" {
			return nil, 0, fmt.Errorf("bd export: %w: %s", waitErr, s)
		}
		return nil, 0, fmt.Errorf("bd export: %w", waitErr)
	case decodeErr != nil:
		return nil, 0, decodeErr
	}
	return byID, rev, nil
}

// decodeExport parses export's line-delimited issues and folds the raw lines
// into a revision hash. The hash covers the bytes as bd emitted them, before
// sanitizing, so it tracks the source data rather than our rendering of it.
func decodeExport(r io.Reader) (map[string]Issue, uint64, error) {
	byID := map[string]Issue{}
	h := fnv.New64a()
	sc := bufio.NewScanner(io.LimitReader(r, maxExport))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var is Issue
		if err := json.Unmarshal(line, &is); err != nil {
			return nil, 0, fmt.Errorf("decode export line: %w", err)
		}
		h.Write(line)
		is.Title = sanitize(is.Title)
		is.Description = sanitize(is.Description)
		is.Notes = sanitize(is.Notes)
		for j, l := range is.Labels {
			is.Labels[j] = sanitize(l)
		}
		byID[is.ID] = is
	}
	if err := sc.Err(); err != nil {
		return nil, 0, fmt.Errorf("read export: %w", err)
	}
	return byID, h.Sum64(), nil
}

// Ready returns the issues that are ready to work — open with no active
// blockers — via `bd ready --json`, which emits a single JSON array rather than
// export's line-delimited objects. Untrusted text fields are sanitized here, as
// in Load, so no downstream consumer has to.
func (c *Client) Ready(ctx context.Context) ([]Issue, error) {
	cmd := exec.CommandContext(ctx, "bd", "ready", "--json")
	cmd.Dir = c.Dir
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			stderr = sanitize(strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("bd ready: %w: %s", err, stderr)
	}

	var issues []Issue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("decode bd ready: %w", err)
	}
	for i := range issues {
		issues[i].Title = sanitize(issues[i].Title)
		issues[i].Description = sanitize(issues[i].Description)
		issues[i].Notes = sanitize(issues[i].Notes)
		for j, l := range issues[i].Labels {
			issues[i].Labels[j] = sanitize(l)
		}
	}
	return issues, nil
}

// Comments returns an issue's activity timeline, oldest first, via
// `bd comments <id> --json`. Untrusted text is sanitized here, as in Load, so no
// downstream consumer has to.
func (c *Client) Comments(ctx context.Context, id string) ([]Comment, error) {
	cmd := exec.CommandContext(ctx, "bd", "comments", id, "--json")
	cmd.Dir = c.Dir
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			stderr = sanitize(strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("bd comments: %w: %s", err, stderr)
	}

	var comments []Comment
	if err := json.Unmarshal(out, &comments); err != nil {
		return nil, fmt.Errorf("decode bd comments: %w", err)
	}
	for i := range comments {
		comments[i].Text = sanitize(comments[i].Text)
		comments[i].Author = sanitize(comments[i].Author)
	}
	return comments, nil
}

// Update persists a single field change via `bd update <id> --<field> <value>`.
// The value is passed as one argv element (no shell), so multi-line description
// and notes need no escaping or temp file.
func (c *Client) Update(ctx context.Context, id, field, value string) error {
	cmd := exec.CommandContext(ctx, "bd", "update", id, "--"+field, value)
	cmd.Dir = c.Dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("bd update: %w: %s", err, sanitize(strings.TrimSpace(string(out))))
	}
	return nil
}

// Create adds a manually-authored issue. A non-empty parent creates a child
// task under that epic; argv is passed directly so titles need no shell escaping.
func (c *Client) Create(ctx context.Context, title, issueType, parent string) error {
	cmd := exec.CommandContext(ctx, "bd", createArgs(title, issueType, parent)...)
	cmd.Dir = c.Dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("bd create: %w: %s", err, sanitize(strings.TrimSpace(string(out))))
	}
	return nil
}

func createArgs(title, issueType, parent string) []string {
	args := []string{"create", "--title", title, "--type", issueType}
	if parent != "" {
		args = append(args, "--parent", parent)
	}
	return args
}

// Delete removes an issue via `bd delete --force`, cascading to its dependents
// when cascade is set — required to delete an epic together with its child tasks
// (bd otherwise refuses rather than orphan them).
func (c *Client) Delete(ctx context.Context, id string, cascade bool) error {
	args := []string{"delete", id, "--force"}
	if cascade {
		args = append(args, "--cascade")
	}
	cmd := exec.CommandContext(ctx, "bd", args...)
	cmd.Dir = c.Dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("bd delete: %w: %s", err, sanitize(strings.TrimSpace(string(out))))
	}
	return nil
}

// Comment appends a comment to an issue via `bd comment <id> <body>`. The body
// is passed as one argv element (no shell), so multi-line text needs no escaping.
func (c *Client) Comment(ctx context.Context, id, body string) error {
	cmd := exec.CommandContext(ctx, "bd", "comment", id, body)
	cmd.Dir = c.Dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("bd comment: %w: %s", err, sanitize(strings.TrimSpace(string(out))))
	}
	return nil
}

// Sanitize is sanitize, exported so other layers can apply it at their own
// boundary — an agent's question or result can quote content it read from a diff
// or an issue body, and that text reaches the terminal too.
func Sanitize(s string) string { return sanitize(s) }

// sanitize strips control bytes that could smuggle terminal escape sequences
// (ANSI/OSC — e.g. clipboard writes or title rewrites) out of untrusted issue
// text, while keeping newlines and tabs that legitimately shape descriptions.
func sanitize(s string) string {
	if !strings.ContainsFunc(s, unsafeControl) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if unsafeControl(r) {
			return -1
		}
		return r
	}, s)
}

func unsafeControl(r rune) bool {
	if r == '\n' || r == '\t' {
		return false
	}
	// C0 controls (incl. ESC), DEL, and C1 controls — any of which can begin
	// or carry an escape sequence in a terminal.
	return r < 0x20 || (r >= 0x7f && r <= 0x9f)
}
