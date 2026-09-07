package usage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCodexWindowsUseProviderDurationsAndPreferredBucket(t *testing.T) {
	raw := json.RawMessage(`{"rateLimits":{"primary":{"usedPercent":99}},"rateLimitsByLimitId":{"codex":{"primary":{"usedPercent":0,"windowDurationMins":300,"resetsAt":1800000000},"secondary":{"usedPercent":37,"windowDurationMins":10080,"resetsAt":1800000300}},"reviews":{"primary":{"usedPercent":88}}}}`)
	w, err := parseCodex(raw)
	require.NoError(t, err)
	require.Len(t, w, 2)
	require.Equal(t, "5h", w[0].Name)
	require.Zero(t, w[0].Used)
	require.Equal(t, "7d", w[1].Name)
	require.Equal(t, float64(37), w[1].Used)
	require.Equal(t, int64(1800000000), w[0].ResetsAt.Unix())
	w, err = parseCodex(json.RawMessage(`{"rateLimits":{"primary":null,"secondary":{"usedPercent":12,"windowDurationMins":10080}}}`))
	require.NoError(t, err)
	require.Len(t, w, 1)
	require.Equal(t, "7d", w[0].Name)
	_, err = parseCodex(json.RawMessage(`{"rateLimits":{"primary":{}}}`))
	require.Error(t, err)
}

func TestClaudeUsageToleratesUnknownFieldsAndAbsentWindows(t *testing.T) {
	w, err := parseClaude(strings.NewReader(`{"five_hour":{"utilization":0,"resets_at":"2026-09-07T18:00:00Z"},"seven_day":{"utilization":23.5,"resets_at":"2026-09-14T18:00:00.123Z"},"seven_day_opus":null,"extra_usage":{"is_enabled":true},"tier":"max","unknown":true}`))
	require.NoError(t, err)
	require.Len(t, w, 2)
	require.Zero(t, w[0].Used)
	require.Equal(t, 23.5, w[1].Used)
	require.False(t, w[1].ResetsAt.IsZero())
	_, err = parseClaude(strings.NewReader(`{"five_hour":null,"seven_day":null}`))
	require.Error(t, err)
	_, err = parseClaude(strings.NewReader(`{"five_hour":{"utilization":"secret-token"}}`))
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret-token")
}

func TestCodexAppServerHandshakeDoesNotStartTurn(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "requests")
	t.Setenv("REQUEST_LOG", log)
	script := `#!/bin/sh
read -r request
printf '%s\n' "$request" >> "$REQUEST_LOG"
printf '%s\n' '{"id":1,"result":{}}'
read -r request
printf '%s\n' "$request" >> "$REQUEST_LOG"
read -r request
printf '%s\n' "$request" >> "$REQUEST_LOG"
printf '%s\n' '{"method":"account/updated","params":{}}' '{"id":2,"result":{"rateLimits":{"primary":{"usedPercent":17,"windowDurationMins":300}}}}'
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o755))
	t.Setenv("PATH", dir)
	s := Fetch(context.Background(), "Codex")
	require.Empty(t, s.Error)
	require.Len(t, s.Windows, 1)
	require.Equal(t, float64(17), s.Windows[0].Used)
	raw, err := os.ReadFile(log)
	require.NoError(t, err)
	require.Contains(t, string(raw), "initialize")
	require.Contains(t, string(raw), "account/rateLimits/read")
	require.NotContains(t, string(raw), "thread/start")
	require.NotContains(t, string(raw), "turn/start")
}

func TestCodexTimeoutAndProviderErrorsDoNotLeak(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nread -r request\nprintf '%s\\n' '{\"id\":1,\"error\":{\"message\":\"secret-token\"}}'\n"), 0o755))
	s := Fetch(context.Background(), "Codex")
	require.NotEmpty(t, s.Error)
	require.NotContains(t, s.Error, "secret-token")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nread -r request\nread -r request\n"), 0o755))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	s = Fetch(ctx, "Codex")
	require.NotEmpty(t, s.Error)
	require.Less(t, time.Since(start), time.Second)
}

func TestClaudeCredentialsReadWithoutExposingToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"test-token"}}`), 0o600))
	token, err := claudeToken(context.Background())
	require.NoError(t, err)
	require.Equal(t, "test-token", token)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "override-token")
	token, err = claudeToken(context.Background())
	require.NoError(t, err)
	require.Equal(t, "override-token", token)
}
