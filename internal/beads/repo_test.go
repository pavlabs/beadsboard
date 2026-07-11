package beads

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseGitHubRepo(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{"git@github.com:pavlabs/beadsboard.git", "pavlabs/beadsboard"},
		{"https://github.com/pavlabs/beadsboard.git", "pavlabs/beadsboard"},
		{"https://github.com/pavlabs/beadsboard", "pavlabs/beadsboard"},
		{"ssh://git@github.com/pavlabs/beadsboard.git", "pavlabs/beadsboard"},
		{"git@gitlab.com:pavlabs/beadsboard.git", ""},
		{"git@github.com:pavlabs/beads/extra.git", ""},
		{"", ""},
	} {
		require.Equal(t, tc.want, parseGitHubRepo(tc.url), tc.url)
	}
}

func TestRepoLabel(t *testing.T) {
	require.Equal(t, "web", repoLabel([]string{"type::epic", "repo::web"}))
	require.Equal(t, "", repoLabel([]string{"type::task", "priority::high"}))
	require.Equal(t, "", repoLabel(nil))
}

// A bead with no repo:: label resolves to the root and the default repo — the
// single-repo behavior, guarded so meta-repo support can't regress it.
func TestRepoForFallsBackToSingleRepo(t *testing.T) {
	c := NewClient("/root")
	got := c.RepoFor([]string{"type::task"}, "pavlabs/beadsboard")
	require.Equal(t, RepoTarget{Dir: "/root", GitHub: "pavlabs/beadsboard"}, got)
}

// A repo::<name> label points work at the <root>/<name> subdir.
func TestRepoForLabeledSubdir(t *testing.T) {
	c := NewClient("/root")
	got := c.RepoFor([]string{"repo::web"}, "pavlabs/beadsboard")
	require.Equal(t, "web", got.Name)
	require.Equal(t, "/root/web", got.Dir)
	// GitHub is read from the subdir's origin remote; empty here (no such dir).
}
