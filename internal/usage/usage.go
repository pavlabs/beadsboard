// Package usage reads subscription quotas without starting a model turn.
// Credentials and raw provider responses never leave the adapters.
package usage

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Window struct {
	Name     string    `json:"name"`
	Used     float64   `json:"used_percent"`
	ResetsAt time.Time `json:"resets_at"`
}

type Snapshot struct {
	Provider  string    `json:"provider"`
	Windows   []Window  `json:"windows,omitempty"`
	FetchedAt time.Time `json:"fetched_at"`
	Error     string    `json:"error,omitempty"`
}

// Fetch uses bounded requests and reports safe, actionable errors only.
func Fetch(ctx context.Context, provider string) Snapshot {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	s := Snapshot{Provider: provider}
	var err error
	switch provider {
	case "Codex":
		s.Windows, err = codex(ctx)
	case "Claude":
		s.Windows, err = claude(ctx)
	default:
		err = errors.New("unknown provider")
	}
	if err != nil {
		s.Error = err.Error()
	} else {
		s.FetchedAt = time.Now()
	}
	return s
}

type codexWindow struct {
	Used    *float64 `json:"usedPercent"`
	Minutes *int     `json:"windowDurationMins"`
	Reset   *int64   `json:"resetsAt"`
}

type codexLimits struct {
	Primary   *codexWindow `json:"primary"`
	Secondary *codexWindow `json:"secondary"`
}

func parseCodex(raw json.RawMessage) ([]Window, error) {
	var r struct {
		Limits  codexLimits            `json:"rateLimits"`
		Buckets map[string]codexLimits `json:"rateLimitsByLimitId"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return nil, errors.New("invalid Codex limit response")
	}
	if c, ok := r.Buckets["codex"]; ok {
		r.Limits = c
	}
	var windows []Window
	for i, w := range []*codexWindow{r.Limits.Primary, r.Limits.Secondary} {
		if w == nil || w.Used == nil {
			continue
		}
		name := "short window"
		if i == 1 {
			name = "long window"
		}
		if w.Minutes != nil && *w.Minutes > 0 {
			switch n := *w.Minutes; {
			case n%1440 == 0:
				name = fmt.Sprintf("%dd", n/1440)
			case n%60 == 0:
				name = fmt.Sprintf("%dh", n/60)
			default:
				name = fmt.Sprintf("%dm", n)
			}
		}
		window := Window{Name: name, Used: *w.Used}
		if w.Reset != nil && *w.Reset > 0 {
			window.ResetsAt = time.Unix(*w.Reset, 0)
		}
		windows = append(windows, window)
	}
	if len(windows) == 0 {
		return nil, errors.New("no subscription limits; check codex login")
	}
	return windows, nil
}

// account/rateLimits/read is the app-server account API. No thread or turn is
// created, so checking limits does not spend model quota.
func codex(ctx context.Context) ([]Window, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "app-server")
	cmd.Dir = os.TempDir()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.New("cannot read Codex app server")
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, errors.New("cannot initialize Codex app server")
	}
	if cmd.Start() != nil {
		return nil, errors.New("codex CLI unavailable; install codex and log in")
	}
	defer func() { _ = stdin.Close(); cancel(); _ = cmd.Wait() }()
	scanner := bufio.NewScanner(io.LimitReader(stdout, 2<<20))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	read := func(id int) (json.RawMessage, error) {
		for scanner.Scan() {
			var msg struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if json.Unmarshal(scanner.Bytes(), &msg) != nil || msg.ID != id {
				continue
			}
			if len(msg.Error) > 0 && string(msg.Error) != "null" {
				return nil, errors.New("codex limits unavailable; check login and connection")
			}
			return msg.Result, nil
		}
		return nil, errors.New("codex limits unavailable; check login and connection")
	}
	if _, err = io.WriteString(stdin, "{\"id\":1,\"method\":\"initialize\",\"params\":{\"clientInfo\":{\"name\":\"beadsboard\",\"version\":\"1\"}}}\n"); err != nil {
		return nil, errors.New("cannot initialize Codex app server")
	}
	if _, err = read(1); err != nil {
		return nil, err
	}
	if _, err = io.WriteString(stdin, "{\"method\":\"initialized\"}\n{\"id\":2,\"method\":\"account/rateLimits/read\"}\n"); err != nil {
		return nil, errors.New("cannot request Codex limits")
	}
	raw, err := read(2)
	if err != nil {
		return nil, err
	}
	return parseCodex(raw)
}

func claudeToken(ctx context.Context) (string, error) {
	if token := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); token != "" {
		return token, nil
	}
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	custom := dir != ""
	if !custom {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("claude account directory unavailable")
		}
		dir = filepath.Join(home, ".claude")
	}
	decode := func(raw []byte) string {
		var r struct {
			OAuth struct {
				AccessToken string `json:"accessToken"`
			} `json:"claudeAiOauth"`
		}
		if json.Unmarshal(raw, &r) != nil {
			return ""
		}
		return r.OAuth.AccessToken
	}
	if raw, err := os.ReadFile(filepath.Join(dir, ".credentials.json")); err == nil {
		if token := decode(raw); token != "" {
			return token, nil
		}
	}
	if runtime.GOOS == "darwin" && !custom {
		// Capture only the CLI's named credential. Never log keychain output.
		raw, err := exec.CommandContext(ctx, "security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Output()
		if err == nil {
			if token := decode(raw); token != "" {
				return token, nil
			}
		}
	}
	return "", errors.New("claude subscription login unavailable; run claude auth login")
}

type claudeWindow struct {
	Used  *float64 `json:"utilization"`
	Reset string   `json:"resets_at"`
}

func parseClaude(reader io.Reader) ([]Window, error) {
	var r map[string]json.RawMessage
	if json.NewDecoder(io.LimitReader(reader, 1<<20)).Decode(&r) != nil {
		return nil, errors.New("invalid Claude limit response")
	}
	var windows []Window
	for _, field := range [][2]string{{"five_hour", "5h"}, {"seven_day", "7d"}, {"seven_day_opus", "Opus 7d"}, {"seven_day_sonnet", "Sonnet 7d"}} {
		var w *claudeWindow
		if raw, ok := r[field[0]]; ok {
			if json.Unmarshal(raw, &w) != nil {
				return nil, errors.New("invalid Claude usage window")
			}
		}
		if w == nil || w.Used == nil {
			continue
		}
		reset, _ := time.Parse(time.RFC3339, w.Reset)
		windows = append(windows, Window{Name: field[1], Used: *w.Used, ResetsAt: reset})
	}
	if len(windows) == 0 {
		return nil, errors.New("no Claude subscription limits reported")
	}
	return windows, nil
}

func claude(ctx context.Context) ([]Window, error) {
	token, err := claudeToken(ctx)
	if err != nil {
		return nil, err
	}
	// This is the endpoint used by Claude Code's /usage command, not the
	// public API billing endpoint. Keep its compatibility surface isolated.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.anthropic.com/api/oauth/usage", nil)
	if err != nil {
		return nil, errors.New("cannot request Claude limits")
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("claude limits unavailable; check connection")
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
		return parseClaude(resp.Body)
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, errors.New("claude login expired or lacks usage access; run claude auth login")
	case http.StatusTooManyRequests:
		return nil, errors.New("claude usage endpoint rate limited; retrying later")
	default:
		return nil, fmt.Errorf("claude usage endpoint returned HTTP %d", resp.StatusCode)
	}
}
