// Package dispatch selects dependency-ready beads for agent execution.
package dispatch

import (
	"context"
	"sync"

	"github.com/pavlabs/beadsboard/internal/agentreg"
	"github.com/pavlabs/beadsboard/internal/beads"
)

type ReadySource interface {
	Ready(context.Context) ([]beads.Issue, error)
}
type Registry interface {
	List() ([]agentreg.Record, error)
}

// Queue admits ready beads without exceeding the configured process limit.
// It only chooses work; the caller owns backend selection and spawning.
type Queue struct {
	ready ReadySource
	reg   Registry
}

func New(ready ReadySource, reg Registry) *Queue { return &Queue{ready: ready, reg: reg} }

// Admit returns ready beads that may be launched now. Live registry records
// consume capacity and suppress their own bead, preventing two board processes
// from dispatching the same work. A non-positive maxAgents means unlimited,
// matching agent.Manager's concurrency semantics.
func (q *Queue) Admit(ctx context.Context, maxAgents int) ([]beads.Issue, error) {
	return q.admit(ctx, maxAgents, nil)
}

// AdmitSubset applies the same capacity and live-agent checks as Admit, but
// limits selection to one dispatch campaign (typically an epic's task IDs).
// Filtering happens before capacity is filled, so unrelated ready work cannot
// crowd the campaign out.
func (q *Queue) AdmitSubset(ctx context.Context, maxAgents int, ids map[string]bool) ([]beads.Issue, error) {
	return q.admit(ctx, maxAgents, ids)
}

func (q *Queue) admit(ctx context.Context, maxAgents int, ids map[string]bool) ([]beads.Issue, error) {
	ready, err := q.ready.Ready(ctx)
	if err != nil {
		return nil, err
	}
	records, err := q.reg.List()
	if err != nil {
		return nil, err
	}

	live := 0
	busy := make(map[string]bool, len(records))
	for _, rec := range records {
		if !rec.Alive() {
			continue
		}
		live++
		if rec.BeadID != "" {
			busy[rec.BeadID] = true
		}
	}

	capacity := len(ready)
	if maxAgents > 0 {
		capacity = maxAgents - live
		if capacity <= 0 {
			return nil, nil
		}
	}

	admitted := make([]beads.Issue, 0, min(capacity, len(ready)))
	seen := make(map[string]bool, len(ready))
	for _, issue := range ready {
		if issue.ID == "" || (ids != nil && !ids[issue.ID]) || busy[issue.ID] || seen[issue.ID] {
			continue
		}
		seen[issue.ID] = true
		admitted = append(admitted, issue)
		if len(admitted) == capacity {
			break
		}
	}
	return admitted, nil
}

// Campaign remembers which tasks belong to an auto-dispatch run and which have
// already been admitted. Reevaluate can therefore be called on every agent or
// bead-state event without dispatching a task twice. Newly ready dependents are
// admitted on a later call after their blockers close.
type Campaign struct {
	mu         sync.Mutex
	queue      *Queue
	candidates map[string]bool
	dispatched map[string]bool
}

func NewCampaign(queue *Queue) *Campaign { return &Campaign{queue: queue} }

func (c *Campaign) Start(ids []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.candidates = make(map[string]bool, len(ids))
	for _, id := range ids {
		if id != "" {
			c.candidates[id] = true
		}
	}
	c.dispatched = map[string]bool{}
}

func (c *Campaign) Active() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.candidates) > 0
}

// NeedsEvaluation is false once every campaign task has been admitted, so
// later unrelated agent events do not keep shelling out to bd ready forever.
func (c *Campaign) NeedsEvaluation() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.candidates {
		if !c.dispatched[id] {
			return true
		}
	}
	return false
}

func (c *Campaign) Reevaluate(ctx context.Context, maxAgents int) ([]beads.Issue, error) {
	if c == nil {
		return nil, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.candidates) == 0 {
		return nil, nil
	}
	remaining := make(map[string]bool, len(c.candidates))
	for id := range c.candidates {
		if !c.dispatched[id] {
			remaining[id] = true
		}
	}
	if len(remaining) == 0 {
		return nil, nil
	}
	admitted, err := c.queue.AdmitSubset(ctx, maxAgents, remaining)
	if err != nil {
		return nil, err
	}
	for _, issue := range admitted {
		c.dispatched[issue.ID] = true
	}
	return admitted, nil
}

// Pending exposes a copy for the UI's upcoming queued/running/blocked labels.
func (c *Campaign) Pending() map[string]bool {
	pending := map[string]bool{}
	if c == nil {
		return pending
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.candidates {
		if !c.dispatched[id] {
			pending[id] = true
		}
	}
	return pending
}
