package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Event lines are verbatim `codex exec --json` output shapes.
func TestCodexParse(t *testing.T) {
	b := codexBackend{bin: "codex"}

	cases := []struct {
		name string
		line string
		want Event
		ok   bool
	}{
		{
			name: "thread.started carries the session id",
			line: `{"type":"thread.started","thread_id":"019fa51b-0e61-73c2-82ad-f71749800346"}`,
			want: Event{Session: "019fa51b-0e61-73c2-82ad-f71749800346"},
			ok:   true,
		},
		{
			name: "agent_message is the result",
			line: `{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"4 files."}}`,
			want: Event{Result: "4 files."},
			ok:   true,
		},
		{
			name: "command_execution is progress",
			line: `{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"/bin/zsh -lc ls","exit_code":0}}`,
			want: Event{Progress: "→ /bin/zsh -lc ls"},
			ok:   true,
		},
		{
			name: "file_change lists edited files",
			line: `{"type":"item.completed","item":{"type":"file_change","changes":[{"path":"/tmp/x/hello.txt","kind":"add"}],"status":"completed"}}`,
			want: Event{Progress: "→ add hello.txt"},
			ok:   true,
		},
		{
			name: "turn.completed is ignored",
			line: `{"type":"turn.completed","usage":{"output_tokens":5}}`,
			ok:   false,
		},
		{
			name: "non-json is ignored",
			line: `Reading additional input from stdin...`,
			ok:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := b.Parse([]byte(tc.line))
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestCodexArgs(t *testing.T) {
	b := codexBackend{bin: "codex"}
	require.Equal(t,
		[]string{"exec", "--json", "-s", "workspace-write", "do the thing"},
		b.HeadlessArgs(Spec{Prompt: "do the thing"}))
	require.Equal(t, []string{"resume", "sess-1"}, b.ResumeArgs("sess-1"))
	require.Equal(t, []string{"hello"}, b.InteractiveArgs("hello"))
}
