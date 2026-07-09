package beads

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// maxExport caps how much `bd export` output we read into memory, so a
// pathological repository cannot exhaust the process.
const maxExport = 64 << 20 // 64 MiB

// Client shells out to the `bd` CLI. Commands run with Dir as the working
// directory because bd keys its embedded state to the repository directory.
type Client struct {
	Dir string
}

func NewClient(dir string) *Client { return &Client{Dir: dir} }

// Load returns every issue, fully hydrated, via a single `bd export --all`.
// Each `bd` invocation cold-starts an embedded Dolt engine (~0.3s) and
// concurrent invocations contend, so one bulk export beats per-issue fetches.
// Untrusted text fields are sanitized here so no downstream consumer has to.
func (c *Client) Load(ctx context.Context) (map[string]Issue, error) {
	cmd := exec.CommandContext(ctx, "bd", "export", "--all")
	cmd.Dir = c.Dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("bd export: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("bd export: %w", err)
	}

	byID := map[string]Issue{}
	var decodeErr error
	sc := bufio.NewScanner(io.LimitReader(stdout, maxExport))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var is Issue
		if err := json.Unmarshal(line, &is); err != nil {
			decodeErr = fmt.Errorf("decode export line: %w", err)
			break
		}
		is.Title = sanitize(is.Title)
		is.Description = sanitize(is.Description)
		for j, l := range is.Labels {
			is.Labels[j] = sanitize(l)
		}
		byID[is.ID] = is
	}
	scanErr := sc.Err()
	// Drain any remainder so the child never blocks on a full pipe, then reap it.
	_, _ = io.Copy(io.Discard, stdout)
	waitErr := cmd.Wait()

	switch {
	case waitErr != nil:
		if s := sanitize(strings.TrimSpace(stderr.String())); s != "" {
			return nil, fmt.Errorf("bd export: %w: %s", waitErr, s)
		}
		return nil, fmt.Errorf("bd export: %w", waitErr)
	case decodeErr != nil:
		return nil, decodeErr
	case scanErr != nil:
		return nil, fmt.Errorf("read export: %w", scanErr)
	}
	return byID, nil
}

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
