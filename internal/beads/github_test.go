package beads

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// parseIssueURL splits a GitHub issue URL into owner/repo and number.
func TestParseIssueURL(t *testing.T) {
	repo, n := parseIssueURL("https://github.com/acme/web/issues/42")
	require.Equal(t, "acme/web", repo)
	require.Equal(t, 42, n)

	repo, n = parseIssueURL("https://github.com/acme/web/pull/42")
	require.Equal(t, "", repo)
	require.Equal(t, 0, n)

	repo, n = parseIssueURL("")
	require.Equal(t, "", repo)
	require.Equal(t, 0, n)
}

// boardStatus maps a Projects Status column name back to a bd status, and skips
// unmapped/unset columns.
func TestBoardStatus(t *testing.T) {
	require.Equal(t, "closed", boardStatus("Done"))
	require.Equal(t, "in_progress", boardStatus("In Progress"))
	require.Equal(t, "blocked", boardStatus("Blocked"))
	require.Equal(t, "open", boardStatus("Todo"))
	require.Equal(t, "", boardStatus(""))
	require.Equal(t, "", boardStatus("Icebox"))
}

// issueStatus collapses a GitHub issue's state + status:: label into one bd
// status: closed state wins, else the carrier label, else open.
func TestIssueStatus(t *testing.T) {
	require.Equal(t, "closed", issueStatus("CLOSED", []string{"status::in_progress"}))
	require.Equal(t, "in_progress", issueStatus("OPEN", []string{"type::task", "status::in_progress"}))
	require.Equal(t, "blocked", issueStatus("open", []string{"status::blocked"}))
	require.Equal(t, "open", issueStatus("OPEN", []string{"type::task", "priority::high"}))
}

// GithubNumber pulls the issue number out of the external_ref bd stores (the
// full issue URL), tolerates a bare gh-<n>, and yields 0 for anything else.
func TestGithubNumber(t *testing.T) {
	require.Equal(t, 42, GithubNumber("https://github.com/pavlabs/beadsboard/issues/42"))
	require.Equal(t, 7, GithubNumber("gh-7"))
	require.Equal(t, 0, GithubNumber(""))
	require.Equal(t, 0, GithubNumber("https://example.com/jira/ABC-1"))
	require.Equal(t, 0, GithubNumber("gh-"))
}
