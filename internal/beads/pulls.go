package beads

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// pullsQuery asks for every open PR across an owner's repositories in one call.
// A meta-repo spreads its work over a dozen repos, and one search beats a
// per-repo fan-out; `gh search prs --json` cannot return review, merge or check
// state, so this goes through GraphQL instead.
const pullsQuery = `query($q: String!, $n: Int!) {
  search(query: $q, type: ISSUE, first: $n) {
    nodes {
      ... on PullRequest {
        number title url isDraft updatedAt mergeable reviewDecision headRefName
        isCrossRepository
        author { login }
        repository { nameWithOwner }
        closingIssuesReferences(first: 5) { nodes { url } }
        commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
      }
    }
  }
}`

// maxPulls caps the search page. A board with more open PRs than this has a
// bigger problem than a truncated inbox.
const maxPulls = 100

// reposPerQuery bounds how many `repo:` qualifiers go into one search. A single
// query with 26 of them is fine in practice (~1.1kB), but the limit is not
// documented, so a large meta-repo splits across queries rather than finding out.
const reposPerQuery = 25

// PullRequest is the slice of an open pull request the board reasons about.
// Mergeable, ReviewDecision and Checks carry GitHub's own vocabulary
// (MERGEABLE/CONFLICTING, APPROVED/CHANGES_REQUESTED/REVIEW_REQUIRED,
// SUCCESS/FAILURE/PENDING) rather than a re-coding of it, and are "" when
// GitHub has not computed one yet.
type PullRequest struct {
	Repo           string
	Number         int
	Title          string
	URL            string
	Branch         string
	Author         string // GitHub login that opened it; "" for a deleted account
	Fork           bool   // head repo is not the base repo, so the branch is an outsider's name
	Draft          bool
	Mergeable      string
	ReviewDecision string
	Checks         string
	Updated        time.Time
	Closes         []string // issue URLs this PR closes, for bead correlation
}

// Ref is the human-facing "repo#number", trimmed of the owner since a board is
// scoped to one.
func (p PullRequest) Ref() string {
	repo := p.Repo
	if _, after, ok := strings.Cut(repo, "/"); ok {
		repo = after
	}
	return fmt.Sprintf("%s#%d", repo, p.Number)
}

// PullRequests returns every open pull request in the given repositories. The
// search is scoped to the board's own repos rather than the whole owner, so a
// PR in some unrelated repository under the same org never reaches the inbox.
func (c *Client) PullRequests(ctx context.Context, repos []string) ([]PullRequest, error) {
	var pulls []PullRequest
	for chunk := range slices.Chunk(repos, reposPerQuery) {
		var q strings.Builder
		for _, repo := range chunk {
			q.WriteString("repo:" + repo + " ")
		}
		q.WriteString("is:pr is:open")

		out, err := c.ghAPI(ctx, "graphql",
			"-f", "query="+pullsQuery,
			"-f", "q="+q.String(),
			"-F", fmt.Sprintf("n=%d", maxPulls))
		if err != nil {
			return nil, err
		}
		got, err := decodePulls(out)
		if err != nil {
			return nil, err
		}
		pulls = append(pulls, got...)
	}
	return pulls, nil
}

// BoardRepos lists the distinct GitHub repositories the board's beads live in:
// each bead's repo:: label resolved through its sub-repo remote in a meta-repo,
// or defaultGitHub in a single-repo project. Sorted, so the search query — and
// anything asserting on it — is stable.
func (c *Client) BoardRepos(graph *Graph, defaultGitHub string) []string {
	if graph == nil {
		return nil
	}
	// Resolve one repo per distinct repo:: label, not one per bead: a board asks
	// the same handful of questions 150 times, and each answer costs a git
	// subprocess on a cache miss.
	labels := map[string]bool{}
	for id, is := range graph.Issues {
		if !Synthetic(id) {
			labels[repoLabel(is.Labels)] = true
		}
	}
	seen := map[string]bool{}
	for label := range labels {
		var target RepoTarget
		if label == "" {
			target = c.RepoFor(nil, defaultGitHub)
		} else {
			target = c.RepoFor([]string{repoLabelPrefix + label}, defaultGitHub)
		}
		if target.GitHub != "" {
			seen[target.GitHub] = true
		}
	}
	repos := slices.Collect(maps.Keys(seen))
	slices.Sort(repos)
	return repos
}

func decodePulls(raw []byte) ([]PullRequest, error) {
	var resp struct {
		Data struct {
			Search struct {
				Nodes []struct {
					Number         int    `json:"number"`
					Title          string `json:"title"`
					URL            string `json:"url"`
					IsDraft        bool   `json:"isDraft"`
					UpdatedAt      string `json:"updatedAt"`
					Mergeable      string `json:"mergeable"`
					ReviewDecision string `json:"reviewDecision"`
					HeadRefName    string `json:"headRefName"`
					CrossRepo      bool   `json:"isCrossRepository"`
					Author         struct {
						Login string `json:"login"`
					} `json:"author"`
					Repository struct {
						NameWithOwner string `json:"nameWithOwner"`
					} `json:"repository"`
					ClosingIssuesReferences struct {
						Nodes []struct {
							URL string `json:"url"`
						} `json:"nodes"`
					} `json:"closingIssuesReferences"`
					Commits struct {
						Nodes []struct {
							Commit struct {
								StatusCheckRollup *struct {
									State string `json:"state"`
								} `json:"statusCheckRollup"`
							} `json:"commit"`
						} `json:"nodes"`
					} `json:"commits"`
				} `json:"nodes"`
			} `json:"search"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode pull search: %w", err)
	}

	var pulls []PullRequest
	for _, n := range resp.Data.Search.Nodes {
		if n.Number == 0 {
			continue // a non-Pull node the search matched
		}
		p := PullRequest{
			Repo:           n.Repository.NameWithOwner,
			Number:         n.Number,
			Title:          sanitize(n.Title),
			URL:            n.URL,
			Branch:         sanitize(n.HeadRefName),
			Author:         sanitize(n.Author.Login),
			Fork:           n.CrossRepo,
			Draft:          n.IsDraft,
			Mergeable:      n.Mergeable,
			ReviewDecision: n.ReviewDecision,
		}
		if t, err := time.Parse(time.RFC3339, n.UpdatedAt); err == nil {
			p.Updated = t
		}
		for _, c := range n.Commits.Nodes {
			if c.Commit.StatusCheckRollup != nil {
				p.Checks = c.Commit.StatusCheckRollup.State
			}
		}
		for _, ref := range n.ClosingIssuesReferences.Nodes {
			p.Closes = append(p.Closes, ref.URL)
		}
		pulls = append(pulls, p)
	}
	return pulls, nil
}

// BeadFor resolves the bead a pull request is working, by the two conventions
// the board already establishes: the PR closes the bead's synced issue, or its
// branch was cut by an agent (`beadsboard/<id>-<n>`) or an external session
// (`bead/<id>`). Issue URLs are matched rather than numbers, which collide
// across a meta-repo's sub-repos. Returns "" when neither convention resolves.
//
// A fork's branch name is whatever the outside author typed and carries no
// permission at all, so it is not trusted to name a bead — otherwise anyone
// could push `bead/<id>` and have their PR listed as the work for it.
//
// A closing reference is NOT held to a higher standard: GitHub populates it from
// a "Closes <url>" line in the PR body, which any fork author writes themselves.
// That vector is knowingly left open — the reference at least names an issue that
// exists, and the inbox marks a fork PR with its author (see attention.pullDetail)
// so a claimed bead is visibly claimed from outside. Do not read this as closed.
func BeadFor(p PullRequest, graph *Graph) string {
	if graph == nil {
		return ""
	}
	if len(p.Closes) > 0 {
		closes := make(map[string]bool, len(p.Closes))
		for _, url := range p.Closes {
			closes[url] = true
		}
		for id, is := range graph.Issues {
			if is.ExternalRef != "" && closes[is.ExternalRef] {
				return id
			}
		}
	}
	if p.Fork {
		return ""
	}
	return beadFromBranch(p.Branch, graph)
}

func beadFromBranch(branch string, graph *Graph) string {
	rest, ok := strings.CutPrefix(branch, "beadsboard/")
	if !ok {
		rest, ok = strings.CutPrefix(branch, "bead/")
	}
	if !ok || rest == "" {
		return ""
	}
	if _, known := graph.Issues[rest]; known {
		return rest
	}
	// A spawned branch is the bead id plus exactly one uniquifying "-<n>" segment
	// (see agent.Spawn), so stripping one segment is the whole decode.
	if i := strings.LastIndexByte(rest, '-'); i > 0 {
		if _, known := graph.Issues[rest[:i]]; known {
			return rest[:i]
		}
	}
	return ""
}
