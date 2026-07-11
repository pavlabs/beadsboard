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
