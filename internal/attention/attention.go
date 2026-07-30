// Package attention answers one question for the whole board: what is waiting
// on the user right now? It folds the states that need a human — an agent
// stopped asking something, an agent that errored, a registered agent whose
// process is gone, a bead marked blocked — into one list keyed by bead, so the
// UI can surface them board-wide instead of only under whichever task is hovered.
package attention

import (
	"sort"
	"time"

	"github.com/pavlabs/beadsboard/internal/agent"
	"github.com/pavlabs/beadsboard/internal/agentreg"
	"github.com/pavlabs/beadsboard/internal/beads"
)

// Reason is why an item wants the user. Declaration order is severity order:
// something actively waiting on an answer outranks something merely parked.
type Reason int

const (
	NeedsInput       Reason = iota // an agent stopped to ask a question
	Failed                         // an agent exited with an error
	ChangesRequested               // a reviewer sent a pull request back
	ChecksFailing                  // a pull request's CI is red
	Conflicted                     // a pull request no longer merges cleanly
	ReviewRequired                 // a pull request is waiting on a review
	ReadyToMerge                   // a pull request is approved, green, and still open
	Stalled                        // a registered agent's process is gone, its bead unfinished
	Blocked                        // the bead itself is marked blocked
	Stale                          // a pull request nobody has touched in a while
)

func (r Reason) String() string {
	switch r {
	case NeedsInput:
		return "needs input"
	case Failed:
		return "failed"
	case ChangesRequested:
		return "changes req"
	case ChecksFailing:
		return "checks red"
	case Conflicted:
		return "conflicted"
	case ReviewRequired:
		return "needs review"
	case ReadyToMerge:
		return "ready merge"
	case Stalled:
		return "stalled"
	case Blocked:
		return "blocked"
	case Stale:
		return "stale"
	}
	return "unknown"
}

// Item is one call on the user's attention. Bead is "" only for a pull request
// that resolves to no bead — the work still needs a human, so it is listed
// rather than dropped.
type Item struct {
	Bead    string
	Reason  Reason
	Detail  string    // the agent's question, its error summary, or the PR title
	AgentID string    // agent this came from; "" for bead- and PR-level reasons
	Ref     string    // "repo#number" for a pull request; "" otherwise
	URL     string    // where to open it, for pull requests
	At      time.Time // when the state was entered, for stable ordering
}

// Subject names what an item is about: its bead, or the pull request's ref when
// the PR resolves to no bead. It is also the ordering tiebreak, so an
// unattributed PR doesn't sort ahead of every bead by virtue of an empty id.
func (it Item) Subject() string {
	if it.Bead != "" {
		return it.Bead
	}
	return it.Ref
}

// staleAfter is how long an untouched open pull request waits before it counts
// as needing a nudge.
const staleAfter = 7 * 24 * time.Hour

// Collect folds every source into one board-level list, newest-first within a
// severity. Managed agents win over registry records for the same agent: the
// manager knows why an agent stopped, the registry only knows whether its
// process is alive.
func Collect(views []agent.View, records []agentreg.Record, pulls []beads.PullRequest, graph *beads.Graph, now time.Time) []Item {
	var items []Item
	managed := map[string]bool{}
	for _, v := range views {
		managed[v.ID] = true
		if it, ok := fromView(v); ok {
			items = append(items, it)
		}
	}
	for _, rec := range records {
		if managed[rec.ID] {
			continue
		}
		if it, ok := fromRecord(rec, graph); ok {
			items = append(items, it)
		}
	}
	for _, p := range pulls {
		if it, ok := fromPull(p, graph, now); ok {
			items = append(items, it)
		}
	}
	items = append(items, fromGraph(graph)...)

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Reason != items[j].Reason {
			return items[i].Reason < items[j].Reason
		}
		if !items[i].At.Equal(items[j].At) {
			return items[i].At.After(items[j].At)
		}
		return items[i].Subject() < items[j].Subject()
	})
	return items
}

// fromView reads a managed agent's lifecycle state. Running, intervened and
// cleanly-finished agents need nothing from the user.
func fromView(v agent.View) (Item, bool) {
	switch v.Status {
	case agent.NeedsInput:
		return Item{Bead: v.IssueID, Reason: NeedsInput, Detail: v.Question, AgentID: v.ID, At: v.Ended}, true
	case agent.Failed:
		return Item{Bead: v.IssueID, Reason: Failed, Detail: v.Summary, AgentID: v.ID, At: v.Ended}, true
	}
	return Item{}, false
}

// fromRecord catches agents nobody is supervising: the process is gone but the
// record was never cleaned up, so its bead is stranded mid-flight. A bead that
// already reached closed needs no attention — the agent simply finished.
func fromRecord(rec agentreg.Record, graph *beads.Graph) (Item, bool) {
	if rec.Alive() || beadStatus(graph, rec.BeadID) == "closed" {
		return Item{}, false
	}
	return Item{Bead: rec.BeadID, Reason: Stalled, AgentID: rec.ID, At: rec.StartedAt}, true
}

// fromPull decides whether an open pull request wants a human, and why. The
// checks are ordered by what blocks progress first: a PR that is both red and
// unreviewed needs its build fixed before a review is worth anyone's time. A
// draft is explicitly not ready, so only its staleness can surface.
func fromPull(p beads.PullRequest, graph *beads.Graph, now time.Time) (Item, bool) {
	it := Item{
		Bead:   beads.BeadFor(p, graph),
		Detail: p.Title,
		Ref:    p.Ref(),
		URL:    p.URL,
		At:     p.Updated,
	}
	stale := !p.Updated.IsZero() && now.Sub(p.Updated) > staleAfter

	switch {
	case p.Draft:
		if !stale {
			return Item{}, false
		}
		it.Reason = Stale
	case p.ReviewDecision == "CHANGES_REQUESTED":
		it.Reason = ChangesRequested
	case p.Checks == "FAILURE" || p.Checks == "ERROR":
		it.Reason = ChecksFailing
	case p.Mergeable == "CONFLICTING":
		it.Reason = Conflicted
	case p.ReviewDecision == "APPROVED":
		it.Reason = ReadyToMerge
	case stale:
		it.Reason = Stale
	default:
		// REVIEW_REQUIRED, or a repo with no review rule at all: either way the
		// PR is open, healthy, and waiting on somebody to look at it.
		it.Reason = ReviewRequired
	}
	return it, true
}

// fromGraph surfaces beads the tracker itself flags as blocked, which is the one
// attention source that exists with no agent involved at all.
func fromGraph(graph *beads.Graph) []Item {
	if graph == nil {
		return nil
	}
	var items []Item
	for id, is := range graph.Issues {
		if is.Status == "blocked" {
			// The title is the only clue a bead-level item carries; without it the
			// row is a bare id nobody can act on.
			items = append(items, Item{Bead: id, Reason: Blocked, Detail: is.Title})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Bead < items[j].Bead })
	return items
}

func beadStatus(graph *beads.Graph, id string) string {
	if graph == nil {
		return ""
	}
	return graph.Issues[id].Status
}
