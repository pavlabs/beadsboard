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
	return RepoTarget{Name: name, Dir: dir, GitHub: originRepo(dir)}
}

// repoLabel returns the sub-repo name from a repo::<name> label, or "".
func repoLabel(labels []string) string {
	for _, l := range labels {
		if v, ok := strings.CutPrefix(l, repoLabelPrefix); ok {
			return v
		}
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

// parseGitHubRepo extracts "owner/repo" from a GitHub remote URL — ssh
// (git@github.com:owner/repo.git), https, or ssh:// form — or "" if it isn't a
// GitHub remote.
func parseGitHubRepo(url string) string {
	url = strings.TrimSuffix(url, ".git")
	for _, sep := range []string{"github.com:", "github.com/"} {
		if _, path, ok := strings.Cut(url, sep); ok {
			if path != "" && strings.Count(path, "/") == 1 {
				return path
			}
			return ""
		}
	}
	return ""
}
