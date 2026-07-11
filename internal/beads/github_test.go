package beads

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// GithubNumber pulls the issue number out of the external_ref bd stores (the
// full issue URL), tolerates a bare gh-<n>, and yields 0 for anything else.
func TestGithubNumber(t *testing.T) {
	require.Equal(t, 42, GithubNumber("https://github.com/pavlabs/beadsboard/issues/42"))
	require.Equal(t, 7, GithubNumber("gh-7"))
	require.Equal(t, 0, GithubNumber(""))
	require.Equal(t, 0, GithubNumber("https://example.com/jira/ABC-1"))
	require.Equal(t, 0, GithubNumber("gh-"))
}

// statusUpdateArgs sets the new status, adds its carrier label, and removes the
// carrier label of every other status so exactly one remains.
func TestStatusUpdateArgs(t *testing.T) {
	got := statusUpdateArgs("bd-1", "in_progress", []string{"open", "in_progress", "closed"})
	require.Equal(t, []string{
		"update", "bd-1",
		"--status", "in_progress",
		"--add-label", "status::in_progress",
		"--remove-label", "status::open",
		"--remove-label", "status::closed",
	}, got)
}
