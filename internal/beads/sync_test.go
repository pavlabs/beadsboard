package beads

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func synced(id, title string) Issue {
	return Issue{ID: id, Title: title, IssueType: "task", Status: "open", ExternalRef: "https://github.com/acme/w/issues/1"}
}

// A reload that changed nothing pushes nothing — this is the whole point: the
// board used to resync every bead after every load.
func TestChangedForSyncQuietWhenNothingMoved(t *testing.T) {
	issues := map[string]Issue{"a": synced("a", "Alpha"), "b": synced("b", "Beta")}
	prev, next := BuildGraph(issues), BuildGraph(issues)

	require.Empty(t, ChangedForSync(prev, next))
}

// One edit costs one bead.
func TestChangedForSyncOneEdit(t *testing.T) {
	prev := BuildGraph(map[string]Issue{"a": synced("a", "Alpha"), "b": synced("b", "Beta")})
	edited := synced("a", "Alpha renamed")
	next := BuildGraph(map[string]Issue{"a": edited, "b": synced("b", "Beta")})

	require.Equal(t, []string{"a"}, ChangedForSync(prev, next))
}

// Every field mirrored onto the issue counts as a change; a field that only
// lives in bd does not.
func TestChangedForSyncFields(t *testing.T) {
	base := synced("a", "Alpha")
	cases := map[string]func(Issue) Issue{
		"title":       func(i Issue) Issue { i.Title = "other"; return i },
		"status":      func(i Issue) Issue { i.Status = "closed"; return i },
		"priority":    func(i Issue) Issue { i.Priority = 3; return i },
		"description": func(i Issue) Issue { i.Description = "why"; return i },
		"notes":       func(i Issue) Issue { i.Notes = "note"; return i },
		"labels":      func(i Issue) Issue { i.Labels = []string{"repo::x"}; return i },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			prev := BuildGraph(map[string]Issue{"a": base})
			next := BuildGraph(map[string]Issue{"a": mutate(base)})
			require.Equal(t, []string{"a"}, ChangedForSync(prev, next))
		})
	}

	t.Run("updated_at alone is not a change", func(t *testing.T) {
		touched := base
		touched.UpdatedAt = "2026-07-30T00:00:00Z"
		prev := BuildGraph(map[string]Issue{"a": base})
		next := BuildGraph(map[string]Issue{"a": touched})
		require.Empty(t, ChangedForSync(prev, next))
	})
}

// A bead with no issue yet always needs pushing — that is what creates it.
func TestChangedForSyncIncludesUnsynced(t *testing.T) {
	fresh := Issue{ID: "c", Title: "Gamma", IssueType: "task", Status: "open"}
	issues := map[string]Issue{"a": synced("a", "Alpha"), "c": fresh}
	g := BuildGraph(issues)

	require.Equal(t, []string{"c"}, ChangedForSync(g, g))
}

// The first load of a session has nothing to diff against. Only never-synced
// beads go out; assuming the rest are in step is deliberate, because pushing all
// of them to prove it is what made startup and every edit cost minutes.
func TestChangedForSyncFirstLoad(t *testing.T) {
	next := BuildGraph(map[string]Issue{
		"a": synced("a", "Alpha"),
		"c": {ID: "c", Title: "Gamma", IssueType: "task", Status: "open"},
	})

	require.Equal(t, []string{"c"}, ChangedForSync(nil, next))
}

// A bead that appeared since the last load is pushed even if it already carries
// an issue — the board has never reconciled it.
func TestChangedForSyncNewBead(t *testing.T) {
	prev := BuildGraph(map[string]Issue{"a": synced("a", "Alpha")})
	next := BuildGraph(map[string]Issue{"a": synced("a", "Alpha"), "b": synced("b", "Beta")})

	require.Equal(t, []string{"b"}, ChangedForSync(prev, next))
}

func TestChangedForSyncNilNext(t *testing.T) {
	require.Nil(t, ChangedForSync(BuildGraph(nil), nil))
}

// The synthetic orphan epic is a display device, not a bead. It has no external
// ref, so without an explicit exclusion it looks like an issue to create — and a
// single-repo project would file a GitHub issue for a placeholder.
func TestChangedForSyncSkipsOrphanBucket(t *testing.T) {
	// A task whose parent epic is absent lands in the orphan bucket.
	next := BuildGraph(map[string]Issue{
		"x.1": {ID: "x.1", Title: "stranded", IssueType: "task", Status: "open", ExternalRef: "https://github.com/acme/w/issues/2", Dependencies: []Dep{{DependsOnID: "x", Type: "parent-child"}}},
	})
	require.Contains(t, next.Issues, orphanEpicID, "precondition: the bucket exists")

	require.NotContains(t, ChangedForSync(nil, next), orphanEpicID)
	require.NotContains(t, ChangedForSync(next, next), orphanEpicID)
}
