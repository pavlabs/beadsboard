package attention

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pavlabs/beadsboard/internal/agent"
	"github.com/pavlabs/beadsboard/internal/agentreg"
	"github.com/pavlabs/beadsboard/internal/beads"
)

func graphWith(issues map[string]beads.Issue) *beads.Graph { return beads.BuildGraph(issues) }

// The states that want a human are picked up; running and finished agents are not.
func TestCollectFromManagedAgents(t *testing.T) {
	views := []agent.View{
		{ID: "a1", IssueID: "bd-1", Status: agent.NeedsInput, Question: "which region?"},
		{ID: "a2", IssueID: "bd-2", Status: agent.Failed, Summary: "build broke"},
		{ID: "a3", IssueID: "bd-3", Status: agent.Running},
		{ID: "a4", IssueID: "bd-4", Status: agent.Done},
		{ID: "a5", IssueID: "bd-5", Status: agent.Intervened},
	}
	got := Collect(views, nil, nil, nil, graphWith(nil), time.Time{})

	require.Len(t, got, 2)
	require.Equal(t, Item{Bead: "bd-1", Reason: NeedsInput, Detail: "which region?", AgentID: "a1"}, got[0])
	require.Equal(t, Item{Bead: "bd-2", Reason: Failed, Detail: "build broke", AgentID: "a2"}, got[1])
}

// A registry record whose process is gone strands its bead, unless the bead
// already closed — then the agent simply finished and nobody is waiting.
// Liveness is supplied by the caller, which computes it off the render path.
func TestCollectFromRegistry(t *testing.T) {
	dead := agentreg.Record{ID: "r1", BeadID: "bd-1"}
	finished := agentreg.Record{ID: "r2", BeadID: "bd-2"}
	running := agentreg.Record{ID: "r3", BeadID: "bd-3"}
	alive := map[string]bool{"r3": true}
	g := graphWith(map[string]beads.Issue{
		"bd-1": {ID: "bd-1", Status: "in_progress", IssueType: "task"},
		"bd-2": {ID: "bd-2", Status: "closed", IssueType: "task"},
		"bd-3": {ID: "bd-3", Status: "in_progress", IssueType: "task"},
	})

	got := Collect(nil, []agentreg.Record{dead, finished, running}, alive, nil, g, time.Time{})

	require.Len(t, got, 1)
	require.Equal(t, "bd-1", got[0].Bead)
	require.Equal(t, Stalled, got[0].Reason)
}

// A record missing from the liveness map reads as not running, so an agent the
// registry knows about but the snapshot missed still surfaces rather than
// silently counting as healthy.
func TestUnknownLivenessCountsAsStalled(t *testing.T) {
	rec := agentreg.Record{ID: "r9", BeadID: "bd-1"}
	g := graphWith(map[string]beads.Issue{"bd-1": {ID: "bd-1", Status: "in_progress", IssueType: "task"}})

	got := Collect(nil, []agentreg.Record{rec}, map[string]bool{}, nil, g, time.Time{})

	require.Len(t, got, 1)
	require.Equal(t, Stalled, got[0].Reason)
}

// The manager's account of an agent wins: it knows why the agent stopped, the
// registry only knows the process is gone. The same agent must not appear twice.
func TestManagedAgentNotDoubleCountedFromRegistry(t *testing.T) {
	views := []agent.View{{ID: "a1", IssueID: "bd-1", Status: agent.NeedsInput, Question: "?"}}
	records := []agentreg.Record{{ID: "a1", BeadID: "bd-1", PID: -1}}
	g := graphWith(map[string]beads.Issue{"bd-1": {ID: "bd-1", Status: "in_progress", IssueType: "task"}})

	got := Collect(views, records, nil, nil, g, time.Time{})

	require.Len(t, got, 1)
	require.Equal(t, NeedsInput, got[0].Reason)
}

// Blocked beads are an attention source with no agent behind them at all.
func TestCollectBlockedBeads(t *testing.T) {
	g := graphWith(map[string]beads.Issue{
		"bd-1": {ID: "bd-1", Title: "blocked epic", Status: "blocked", IssueType: "epic"},
		"bd-2": {ID: "bd-2", Status: "open", IssueType: "task"},
	})

	got := Collect(nil, nil, nil, nil, g, time.Time{})

	require.Len(t, got, 1)
	require.Equal(t, Item{Bead: "bd-1", Reason: Blocked, Detail: "blocked epic"}, got[0])
}

// Severity leads the ordering, so whatever is actively waiting on an answer sits
// at the top of the inbox; ties break newest-first.
func TestCollectOrdersBySeverityThenRecency(t *testing.T) {
	old := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	recent := old.Add(time.Hour)
	views := []agent.View{
		{ID: "a1", IssueID: "bd-9", Status: agent.Failed, Ended: old},
		{ID: "a2", IssueID: "bd-8", Status: agent.NeedsInput, Ended: old},
		{ID: "a3", IssueID: "bd-7", Status: agent.NeedsInput, Ended: recent},
	}
	g := graphWith(map[string]beads.Issue{"bd-0": {ID: "bd-0", Status: "blocked", IssueType: "task"}})

	got := Collect(views, nil, nil, nil, g, time.Time{})

	require.Equal(t, []string{"bd-7", "bd-8", "bd-9", "bd-0"}, beadsOf(got))
	require.Equal(t, []Reason{NeedsInput, NeedsInput, Failed, Blocked}, reasonsOf(got))
}

// An idle board produces nothing, so the UI can treat empty as "nothing wants you".
func TestCollectEmpty(t *testing.T) {
	require.Empty(t, Collect(nil, nil, nil, nil, graphWith(nil), time.Time{}))
	require.Empty(t, Collect(nil, nil, nil, nil, nil, time.Time{}))
}

func TestReasonLabels(t *testing.T) {
	require.Equal(t, "needs input", NeedsInput.String())
	require.Equal(t, "failed", Failed.String())
	require.Equal(t, "stalled", Stalled.String())
	require.Equal(t, "blocked", Blocked.String())
}

func beadsOf(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Bead
	}
	return out
}

func reasonsOf(items []Item) []Reason {
	out := make([]Reason, len(items))
	for i, it := range items {
		out[i] = it.Reason
	}
	return out
}

// Each pull-request state maps to the reason that names it. Order matters where
// states overlap: a red build outranks a pending review, because fixing the
// build comes first.
func TestCollectFromPulls(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-time.Hour)
	pull := func(p beads.PullRequest) beads.PullRequest {
		p.Repo, p.Number, p.Updated = "acme/w", 1, fresh
		return p
	}
	cases := []struct {
		name string
		pr   beads.PullRequest
		want Reason
	}{
		{"waiting on review", pull(beads.PullRequest{ReviewDecision: "REVIEW_REQUIRED"}), ReviewRequired},
		{"no review rule", pull(beads.PullRequest{}), ReviewRequired},
		{"changes requested", pull(beads.PullRequest{ReviewDecision: "CHANGES_REQUESTED"}), ChangesRequested},
		{"checks red", pull(beads.PullRequest{Checks: "FAILURE"}), ChecksFailing},
		{"checks errored", pull(beads.PullRequest{Checks: "ERROR"}), ChecksFailing},
		{"conflicted", pull(beads.PullRequest{Mergeable: "CONFLICTING"}), Conflicted},
		{"approved, unmerged", pull(beads.PullRequest{ReviewDecision: "APPROVED"}), ReadyToMerge},
		{"red beats unreviewed", pull(beads.PullRequest{Checks: "FAILURE", ReviewDecision: "REVIEW_REQUIRED"}), ChecksFailing},
		{"changes requested beats red", pull(beads.PullRequest{Checks: "FAILURE", ReviewDecision: "CHANGES_REQUESTED"}), ChangesRequested},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Collect(nil, nil, nil, []beads.PullRequest{tc.pr}, graphWith(nil), now)
			require.Len(t, got, 1)
			require.Equal(t, tc.want, got[0].Reason)
			require.Equal(t, "w#1", got[0].Ref)
		})
	}
}

// A draft is explicitly not ready, so it stays out of the inbox — until nobody
// has touched it for long enough that it looks abandoned.
func TestDraftPullsOnlySurfaceWhenStale(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	active := beads.PullRequest{Repo: "acme/w", Number: 1, Draft: true, Updated: now.Add(-time.Hour)}
	abandoned := beads.PullRequest{Repo: "acme/w", Number: 2, Draft: true, Updated: now.Add(-30 * 24 * time.Hour)}

	require.Empty(t, Collect(nil, nil, nil, []beads.PullRequest{active}, graphWith(nil), now))

	got := Collect(nil, nil, nil, []beads.PullRequest{abandoned}, graphWith(nil), now)
	require.Len(t, got, 1)
	require.Equal(t, Stale, got[0].Reason)
}

// An untouched open PR is a nudge, not an emergency: it sorts below everything
// actively broken.
func TestStalePullSortsLast(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	pulls := []beads.PullRequest{
		{Repo: "acme/w", Number: 1, Updated: now.Add(-30 * 24 * time.Hour)},
		{Repo: "acme/w", Number: 2, Checks: "FAILURE", Updated: now.Add(-time.Hour)},
	}

	got := Collect(nil, nil, nil, pulls, graphWith(nil), now)

	require.Equal(t, []Reason{ChecksFailing, Stale}, reasonsOf(got))
}

// A PR carries its bead when one resolves, and is still listed when none does —
// unattributed work needs a human just as much.
func TestPullCarriesBeadWhenResolvable(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	g := graphWith(map[string]beads.Issue{"bd-1": {ID: "bd-1", IssueType: "task"}})
	pulls := []beads.PullRequest{
		{Repo: "acme/w", Number: 1, Branch: "beadsboard/bd-1", Updated: now},
		{Repo: "acme/w", Number: 2, Branch: "feat/whatever", Updated: now},
	}

	got := Collect(nil, nil, nil, pulls, g, now)

	require.Len(t, got, 2)
	require.Equal(t, "bd-1", got[0].Bead)
	require.Empty(t, got[1].Bead)
	require.Equal(t, "w#2", got[1].Ref)
}
