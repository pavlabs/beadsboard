package beads

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A real `gh api graphql` response for an org's open PRs, trimmed to three
// nodes; the third is a fork PR whose branch imitates the board's convention.
const pullsResponse = `{"data":{"search":{"nodes":[
 {"number":8,"title":"feat: add job-spec v0 contract","url":"https://github.com/zoomie-build/proto/pull/8",
  "isDraft":false,"updatedAt":"2026-07-29T17:38:00Z","mergeable":"MERGEABLE","reviewDecision":null,
  "headRefName":"feat/jobspec-v0","isCrossRepository":false,"author":{"login":"artemijspavlovs"},
  "repository":{"nameWithOwner":"zoomie-build/proto"},
  "closingIssuesReferences":{"nodes":[]},
  "commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"FAILURE"}}}]}},
 {"number":7,"title":"docs: record job-spec v0 schema design","url":"https://github.com/zoomie-build/proto/pull/7",
  "isDraft":false,"updatedAt":"2026-07-13T16:27:15Z","mergeable":"MERGEABLE","reviewDecision":null,
  "headRefName":"beadsboard/zoomie-hgt.1-2","isCrossRepository":false,"author":{"login":"artemijspavlovs"},
  "repository":{"nameWithOwner":"zoomie-build/proto"},
  "closingIssuesReferences":{"nodes":[{"url":"https://github.com/zoomie-build/proto/issues/5"}]},
  "commits":{"nodes":[{"commit":{"statusCheckRollup":null}}]}},
 {"number":9,"title":"chore: tidy","url":"https://github.com/zoomie-build/proto/pull/9",
  "isDraft":false,"updatedAt":"2026-07-30T09:02:41Z","mergeable":"MERGEABLE","reviewDecision":null,
  "headRefName":"beadsboard/zoomie-hgt.1-3","isCrossRepository":true,"author":{"login":"drive-by"},
  "repository":{"nameWithOwner":"zoomie-build/proto"},
  "closingIssuesReferences":{"nodes":[]},
  "commits":{"nodes":[{"commit":{"statusCheckRollup":null}}]}}
]}}}`

func TestDecodePulls(t *testing.T) {
	got, err := decodePulls([]byte(pullsResponse))
	require.NoError(t, err)
	require.Len(t, got, 3)

	require.Equal(t, "zoomie-build/proto", got[0].Repo)
	require.Equal(t, 8, got[0].Number)
	require.Equal(t, "proto#8", got[0].Ref())
	require.Equal(t, "FAILURE", got[0].Checks)
	require.False(t, got[0].Draft)
	require.Equal(t, "artemijspavlovs", got[0].Author)
	require.False(t, got[0].Fork)
	require.Equal(t, time.Date(2026, 7, 29, 17, 38, 0, 0, time.UTC), got[0].Updated.UTC())
	require.Empty(t, got[0].Closes)

	// A null statusCheckRollup is "no checks configured", not a failure.
	require.Empty(t, got[1].Checks)
	require.Equal(t, []string{"https://github.com/zoomie-build/proto/issues/5"}, got[1].Closes)

	require.True(t, got[2].Fork)
	require.Equal(t, "drive-by", got[2].Author)
}

// A deleted account decodes as a null author, which is a PR like any other
// rather than a decode failure.
func TestDecodePullsWithoutAuthor(t *testing.T) {
	got, err := decodePulls([]byte(`{"data":{"search":{"nodes":[
	 {"number":1,"title":"t","url":"https://github.com/acme/w/pull/1","author":null,
	  "repository":{"nameWithOwner":"acme/w"}}]}}}`))
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Empty(t, got[0].Author)
}

// An empty search is not an error — a board with no open PRs is normal.
func TestDecodePullsEmpty(t *testing.T) {
	got, err := decodePulls([]byte(`{"data":{"search":{"nodes":[]}}}`))
	require.NoError(t, err)
	require.Empty(t, got)
}

// A PR is matched to its bead by the issue it closes, since issue numbers
// collide across a meta-repo's sub-repos but URLs do not.
func TestBeadForByClosingIssueURL(t *testing.T) {
	g := BuildGraph(map[string]Issue{
		"zoomie-hgt.1": {ID: "zoomie-hgt.1", IssueType: "task", ExternalRef: "https://github.com/zoomie-build/proto/issues/5"},
		"other-9":      {ID: "other-9", IssueType: "task", ExternalRef: "https://github.com/zoomie-build/infra/issues/5"},
	})
	pulls, err := decodePulls([]byte(pullsResponse))
	require.NoError(t, err)

	require.Equal(t, "zoomie-hgt.1", BeadFor(pulls[1], g))
}

// Falling back to the branch: agents cut `beadsboard/<id>-<n>`, external
// sessions cut `bead/<id>`, and an unrelated branch resolves to nothing.
func TestBeadForByBranch(t *testing.T) {
	g := BuildGraph(map[string]Issue{
		"zoomie-hgt.1": {ID: "zoomie-hgt.1", IssueType: "task"},
		"bd-7":         {ID: "bd-7", IssueType: "task"},
	})

	require.Equal(t, "zoomie-hgt.1", BeadFor(PullRequest{Branch: "beadsboard/zoomie-hgt.1-2"}, g))
	require.Equal(t, "zoomie-hgt.1", BeadFor(PullRequest{Branch: "beadsboard/zoomie-hgt.1"}, g))
	require.Equal(t, "bd-7", BeadFor(PullRequest{Branch: "bead/bd-7"}, g))
	require.Equal(t, "bd-7", BeadFor(PullRequest{Branch: "beadsboard/bd-7-1"}, g))
	require.Empty(t, BeadFor(PullRequest{Branch: "feat/jobspec-v0"}, g))
	require.Empty(t, BeadFor(PullRequest{Branch: "beadsboard/unknown-3"}, g))
	require.Empty(t, BeadFor(PullRequest{Branch: "bead/bd-7"}, nil))
}

// The closing issue wins over the branch when the two disagree: an explicit
// `Closes #N` is a statement of intent, a branch name is a convention.
func TestBeadForPrefersClosingIssue(t *testing.T) {
	g := BuildGraph(map[string]Issue{
		"bd-1": {ID: "bd-1", IssueType: "task", ExternalRef: "https://github.com/acme/w/issues/3"},
		"bd-2": {ID: "bd-2", IssueType: "task"},
	})
	p := PullRequest{Branch: "beadsboard/bd-2", Closes: []string{"https://github.com/acme/w/issues/3"}}

	require.Equal(t, "bd-1", BeadFor(p, g))
}

// A branch name on a fork is whatever an outsider typed, so it must not claim a
// bead — otherwise any PR from anyone lands in the inbox as that bead's work.
// A closing reference is a different thing: it names an issue that exists, so it
// still counts however the PR arrived.
func TestBeadForIgnoresForkBranch(t *testing.T) {
	g := BuildGraph(map[string]Issue{
		"zoomie-hgt.1": {ID: "zoomie-hgt.1", IssueType: "task", ExternalRef: "https://github.com/zoomie-build/proto/issues/5"},
	})
	pulls, err := decodePulls([]byte(pullsResponse))
	require.NoError(t, err)

	require.Empty(t, BeadFor(pulls[2], g))

	claimed := pulls[2]
	claimed.Closes = []string{"https://github.com/zoomie-build/proto/issues/5"}
	require.Equal(t, "zoomie-hgt.1", BeadFor(claimed, g))
}

// No repos in scope means no call to make.
func TestPullRequestsWithoutRepos(t *testing.T) {
	got, err := NewClient(".").PullRequests(t.Context(), nil)
	require.NoError(t, err)
	require.Nil(t, got)
}

// The search covers exactly the repos the board's beads resolve to: sub-repo
// remotes in a meta-repo, the configured default otherwise, deduped and sorted.
func TestBoardRepos(t *testing.T) {
	c := NewClient(t.TempDir())
	g := BuildGraph(map[string]Issue{
		"a":   {ID: "a", IssueType: "epic"},
		"a.1": {ID: "a.1", IssueType: "task"},
	})

	require.Equal(t, []string{"acme/widgets"}, c.BoardRepos(g, "acme/widgets"))
	require.Nil(t, c.BoardRepos(g, ""), "no default and no labels resolves to nothing")
	require.Nil(t, c.BoardRepos(nil, "acme/widgets"))
}

// A bead labelled for a sub-repo that isn't checked out resolves to no repo, so
// it contributes nothing to the search rather than a malformed qualifier.
func TestBoardReposSkipsUnresolvableSubRepo(t *testing.T) {
	c := NewClient(t.TempDir())
	g := BuildGraph(map[string]Issue{
		"a": {ID: "a", IssueType: "epic", Labels: []string{"repo::missing"}},
		"b": {ID: "b", IssueType: "epic"},
	})

	require.Equal(t, []string{"acme/widgets"}, c.BoardRepos(g, "acme/widgets"))
}

// A remote whose URL carries anything but a plausible owner/repo is refused: the
// parsed value is interpolated into API paths and into the PR search query.
func TestParseGitHubRepoRejectsHostileRemotes(t *testing.T) {
	require.Equal(t, "acme/widgets", parseGitHubRepo("git@github.com:acme/widgets.git"))
	require.Equal(t, "acme/widgets", parseGitHubRepo("https://github.com/acme/widgets"))

	require.Empty(t, parseGitHubRepo("https://github.com/acme/w is:closed"), "query injection")
	require.Empty(t, parseGitHubRepo("https://github.com/../.."), "path traversal")
	require.Empty(t, parseGitHubRepo("https://github.com/acme"), "no repo segment")
	require.Empty(t, parseGitHubRepo("https://github.com/acme/w/extra"))
	require.Empty(t, parseGitHubRepo("https://gitlab.com/acme/widgets"))
}

// A repo:: label picks the subdirectory an agent is worktreed and run in, so it
// must be a single plain path segment.
func TestRepoLabelRejectsTraversal(t *testing.T) {
	require.Equal(t, "proto", repoLabel([]string{"repo::proto"}))
	require.Empty(t, repoLabel([]string{"repo::../../../elsewhere"}))
	require.Empty(t, repoLabel([]string{"repo::.."}))
	require.Empty(t, repoLabel([]string{"repo::a/b"}))
	require.Empty(t, repoLabel([]string{"repo::"}))
	require.Empty(t, repoLabel([]string{"gate"}))
}

// The digest defines what "already pushed" means, so it must move with every
// mirrored field and stay put for the ones bd stamps on its own writes.
func TestSyncDigest(t *testing.T) {
	base := Issue{ID: "a", Title: "T", Status: "open", Priority: 1, Description: "d", Notes: "n", Labels: []string{"x"}}
	require.Equal(t, SyncDigest(base), SyncDigest(base))

	for name, mutate := range map[string]func(Issue) Issue{
		"title":       func(i Issue) Issue { i.Title = "other"; return i },
		"status":      func(i Issue) Issue { i.Status = "closed"; return i },
		"priority":    func(i Issue) Issue { i.Priority = 2; return i },
		"description": func(i Issue) Issue { i.Description = "other"; return i },
		"notes":       func(i Issue) Issue { i.Notes = "other"; return i },
		"labels":      func(i Issue) Issue { i.Labels = []string{"y"}; return i },
	} {
		t.Run(name, func(t *testing.T) {
			require.NotEqual(t, SyncDigest(base), SyncDigest(mutate(base)))
		})
	}

	t.Run("bd's own stamps do not move it", func(t *testing.T) {
		stamped := base
		stamped.ExternalRef = "https://github.com/acme/w/issues/1"
		stamped.UpdatedAt = "2026-07-30T00:00:00Z"
		require.Equal(t, SyncDigest(base), SyncDigest(stamped),
			"including these would make every push look like a fresh change")
	})

	t.Run("field boundaries do not collide", func(t *testing.T) {
		a := Issue{Title: "ab", Status: "c"}
		b := Issue{Title: "a", Status: "bc"}
		require.NotEqual(t, SyncDigest(a), SyncDigest(b))
	})
}
