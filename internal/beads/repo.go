package beads

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// repoLabelPrefix routes a bead to a sub-repo in a meta-repo layout: a
// repo::<name> label (set on the epic, inherited by its tasks) means the work
// lives in the <name> subdirectory and its issue in that subdir's remote.
const repoLabelPrefix = "repo::"

// RepoTarget is where a bead's work and issue live: the local git repo to
// worktree and PR from, and its GitHub "owner/repo". For a bead with no repo::
// label these fall back to the beads root and the configured default repo, which
// is exactly the single-repo behavior.
type RepoTarget struct {
	Name   string // repo:: label value, or "" for the root
	Dir    string // local git repo path: the root, or <root>/<name>
	GitHub string // "owner/repo", or "" if it can't be resolved
}

// RepoFor resolves the repo a bead targets from its labels. With a repo::<name>
// label the work lives in <root>/<name> and its issue in that subdir's origin
// remote; without one it falls back to the root and defaultGitHub.
func (c *Client) RepoFor(labels []string, defaultGitHub string) RepoTarget {
	name := repoLabel(labels)
	if name == "" {
		return RepoTarget{Dir: c.Dir, GitHub: defaultGitHub}
	}
	dir := filepath.Join(c.Dir, name)
	return RepoTarget{Name: name, Dir: dir, GitHub: c.cachedOriginRepo(dir)}
}

// cachedOriginRepo resolves dir's origin remote once per client — see the
// originRepos field on Client for why.
func (c *Client) cachedOriginRepo(dir string) string {
	c.mu.Lock()
	repo, ok := c.originRepos[dir]
	c.mu.Unlock()
	if ok {
		return repo
	}
	// Not under the lock: this shells out to git, and the UI goroutine resolves
	// repos too — holding the mutex across the exec would block a frame behind a
	// background fetch. A duplicate lookup on a cold race is cheaper than that.
	repo = originRepo(dir)
	c.mu.Lock()
	c.originRepos[dir] = repo
	c.mu.Unlock()
	return repo
}

// repoLabel returns the sub-repo name from a repo::<name> label, or "". The value
// names a subdirectory that becomes an agent's working repo, so anything that is
// not a single plain path segment is refused rather than joined onto the root —
// beads are not purely first-party input (they sync over a git remote, and agents
// write them).
func repoLabel(labels []string) string {
	for _, l := range labels {
		v, ok := strings.CutPrefix(l, repoLabelPrefix)
		if !ok {
			continue
		}
		if v == "" || v != filepath.Base(v) || v == ".." || strings.ContainsAny(v, `/\`) {
			return ""
		}
		return v
	}
	return ""
}

// originRepo returns the "owner/repo" of dir's origin remote, or "" when there
// is no origin or it isn't a GitHub URL.
func originRepo(dir string) string {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return parseGitHubRepo(strings.TrimSpace(string(out)))
}

// validRepoSegment reports whether s is a plausible GitHub owner or repository
// name. Anything else in a remote URL is refused: the parsed "owner/repo" is
// interpolated into API paths and into a search query, so a remote carrying
// spaces or dot segments must not travel any further.
func validRepoSegment(s string) bool {
	// Dots are legal inside a name ("my.repo"), so the dot segments have to be
	// refused explicitly, or "../.." parses as a valid owner/repo pair.
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// parseGitHubRepo extracts "owner/repo" from a GitHub remote URL — ssh
// (git@github.com:owner/repo.git), https, or ssh:// form — or "" if it isn't a
// GitHub remote or either segment isn't a plausible name.
func parseGitHubRepo(url string) string {
	url = strings.TrimSuffix(url, ".git")
	for _, sep := range []string{"github.com:", "github.com/"} {
		_, path, ok := strings.Cut(url, sep)
		if !ok {
			continue
		}
		owner, repo, ok := strings.Cut(path, "/")
		if !ok || strings.Contains(repo, "/") || !validRepoSegment(owner) || !validRepoSegment(repo) {
			return ""
		}
		return path
	}
	return ""
}
