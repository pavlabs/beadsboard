package beads

import (
	"sort"
	"strings"
)

// Task/epic status glyph values used across the UI.
const (
	StatusDone    = "done"
	StatusWIP     = "wip"
	StatusReady   = "ready"
	StatusBlocked = "blocked"
	StatusOpen    = "open"
)

// orphanEpicID is the synthetic epic that collects tasks whose real epic can't
// be resolved, so they surface in the UI instead of vanishing. The '~' prefix
// keeps it clear of real bd ids and sorts it last.
const orphanEpicID = "~orphans"

// Graph is the derived, display-ready view over a set of hydrated issues.
type Graph struct {
	Issues map[string]Issue

	Epics []string            // epic ids, display order (priority, build order)
	Tasks map[string][]string // epic id -> task ids, topological order

	EpicStatus map[string]string // epic id -> done|wip|open
	TaskStatus map[string]string // task id -> done|wip|ready|blocked

	Prereqs map[string][]string // epic id -> epics that must finish first
	Unlocks map[string][]string // epic id -> epics it unblocks

	openBlockers map[string][]string // task id -> unclosed blocker ids
}

// BuildGraph derives epics, per-epic tasks, statuses and the inter-epic DAG.
func BuildGraph(issues map[string]Issue) *Graph {
	g := &Graph{
		Issues:       issues,
		Tasks:        map[string][]string{},
		EpicStatus:   map[string]string{},
		TaskStatus:   map[string]string{},
		Prereqs:      map[string][]string{},
		Unlocks:      map[string][]string{},
		openBlockers: map[string][]string{},
	}

	// Collect epics and group each task under its epic in one pass; tasks whose
	// epic can't be resolved are held aside and surfaced under a synthetic epic.
	var orphans []string
	for id, is := range issues {
		if is.IsEpic() {
			g.Epics = append(g.Epics, id)
			continue
		}
		if e := g.epicOf(id); e != "" {
			g.Tasks[e] = append(g.Tasks[e], id)
		} else {
			orphans = append(orphans, id)
		}
	}
	if len(orphans) > 0 {
		// Priority 9 (past bd's 0-4 range) sorts the bucket last.
		g.Issues[orphanEpicID] = Issue{ID: orphanEpicID, Title: "orphaned tasks", IssueType: "epic", Priority: 9}
		g.Epics = append(g.Epics, orphanEpicID)
		g.Tasks[orphanEpicID] = orphans
	}

	for _, e := range g.Epics {
		g.Tasks[e] = g.topoTasks(g.Tasks[e])
	}

	g.deriveStatuses()
	g.deriveEpicDAG()
	g.orderEpics()
	return g
}

// EpicOf returns the epic an issue belongs to, or "" if it can't be resolved.
func (g *Graph) EpicOf(id string) string { return g.epicOf(id) }

// epicOf maps any issue id to the epic it belongs to: itself if it is an epic,
// otherwise its Parent, falling back to the "<epic>.N" id convention.
func (g *Graph) epicOf(id string) string {
	is, ok := g.Issues[id]
	if !ok {
		return ""
	}
	if is.IsEpic() {
		return id
	}
	if pid := is.parentID(); pid != "" {
		if p, ok := g.Issues[pid]; ok && p.IsEpic() {
			return pid
		}
	}
	if base := idBase(id); base != "" {
		if b, ok := g.Issues[base]; ok && b.IsEpic() {
			return base
		}
	}
	return ""
}

func idBase(id string) string {
	if i := strings.LastIndexByte(id, '.'); i >= 0 {
		return id[:i]
	}
	return ""
}

func (g *Graph) deriveStatuses() {
	for id, is := range g.Issues {
		if is.IsEpic() {
			continue
		}
		var open []string
		for _, b := range is.blockers() {
			if bl, ok := g.Issues[b]; !ok || bl.Status != "closed" {
				open = append(open, b)
			}
		}
		g.openBlockers[id] = open

		switch {
		case is.Status == "closed":
			g.TaskStatus[id] = StatusDone
		case is.Status == "in_progress":
			g.TaskStatus[id] = StatusWIP
		case len(open) == 0:
			g.TaskStatus[id] = StatusReady
		default:
			g.TaskStatus[id] = StatusBlocked
		}
	}

	for _, e := range g.Epics {
		kids := g.Tasks[e]
		g.EpicStatus[e] = epicStatus(g.Issues, kids)
	}
}

func epicStatus(issues map[string]Issue, kids []string) string {
	if len(kids) == 0 {
		return StatusOpen
	}
	allClosed := true
	for _, k := range kids {
		switch issues[k].Status {
		case "in_progress":
			return StatusWIP
		case "closed":
		default:
			allClosed = false
		}
	}
	if allClosed {
		return StatusDone
	}
	return StatusOpen
}

// deriveEpicDAG lifts cross-epic "blocks" edges on tasks to epic-level edges.
func (g *Graph) deriveEpicDAG() {
	pre := map[string]map[string]bool{}
	unl := map[string]map[string]bool{}
	for _, e := range g.Epics {
		pre[e] = map[string]bool{}
		unl[e] = map[string]bool{}
	}
	for id, is := range g.Issues {
		ce := g.epicOf(id)
		if ce == "" {
			continue
		}
		for _, b := range is.blockers() {
			pe := g.epicOf(b)
			if pe != "" && pe != ce {
				pre[ce][pe] = true
				unl[pe][ce] = true
			}
		}
	}
	for _, e := range g.Epics {
		g.Prereqs[e] = keys(pre[e])
		g.Unlocks[e] = keys(unl[e])
	}
}

// OpenBlockerRefs returns the unclosed blockers of a task for display.
func (g *Graph) OpenBlockerRefs(id string) []string { return g.openBlockers[id] }

// EpicProgress returns (closed, total) task counts for an epic.
func (g *Graph) EpicProgress(epic string) (int, int) {
	kids := g.Tasks[epic]
	done := 0
	for _, k := range kids {
		if g.Issues[k].Status == "closed" {
			done++
		}
	}
	return done, len(kids)
}

// topoTasks orders task ids by in-epic "blocks" dependencies (Kahn), with id
// tiebreaks, so prerequisites list before the work they unblock.
func (g *Graph) topoTasks(ids []string) []string {
	inEpic := map[string]bool{}
	for _, id := range ids {
		inEpic[id] = true
	}
	indeg := map[string]int{}
	adj := map[string][]string{}
	for _, id := range ids {
		indeg[id] = 0
	}
	for _, id := range ids {
		for _, b := range g.Issues[id].blockers() {
			if inEpic[b] {
				adj[b] = append(adj[b], id)
				indeg[id]++
			}
		}
	}
	var q []string
	for _, id := range ids {
		if indeg[id] == 0 {
			q = append(q, id)
		}
	}
	sort.Strings(q)
	var out []string
	seen := map[string]bool{}
	for len(q) > 0 {
		n := q[0]
		q = q[1:]
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
		nexts := append([]string(nil), adj[n]...)
		sort.Strings(nexts)
		for _, m := range nexts {
			indeg[m]--
			if indeg[m] == 0 {
				q = append(q, m)
			}
		}
	}
	// Any leftovers (cycles) appended deterministically.
	for _, id := range ids {
		if !seen[id] {
			out = append(out, id)
		}
	}
	return out
}

// orderEpics sorts epics by (priority, build order, title). Build order is a
// Kahn topological index over the epic DAG.
func (g *Graph) orderEpics() {
	indeg := map[string]int{}
	for _, e := range g.Epics {
		indeg[e] = len(g.Prereqs[e])
	}
	var q []string
	for _, e := range g.Epics {
		if indeg[e] == 0 {
			q = append(q, e)
		}
	}
	sort.Slice(q, func(a, b int) bool { return g.prioLess(q[a], q[b]) })
	order := map[string]int{}
	seen := 0
	for len(q) > 0 {
		n := q[0]
		q = q[1:]
		order[n] = seen
		seen++
		nexts := append([]string(nil), g.Unlocks[n]...)
		sort.Slice(nexts, func(a, b int) bool { return g.prioLess(nexts[a], nexts[b]) })
		for _, m := range nexts {
			indeg[m]--
			if indeg[m] == 0 {
				q = append(q, m)
			}
		}
	}
	// Epics left unreached form a dependency cycle; order them after the rest,
	// deterministically, mirroring topoTasks so none are lost or bunched at 0.
	var leftover []string
	for _, e := range g.Epics {
		if _, ok := order[e]; !ok {
			leftover = append(leftover, e)
		}
	}
	sort.Slice(leftover, func(a, b int) bool { return g.prioLess(leftover[a], leftover[b]) })
	for _, e := range leftover {
		order[e] = seen
		seen++
	}
	sort.SliceStable(g.Epics, func(a, b int) bool {
		ea, eb := g.Epics[a], g.Epics[b]
		if g.Issues[ea].Priority != g.Issues[eb].Priority {
			return g.Issues[ea].Priority < g.Issues[eb].Priority
		}
		if order[ea] != order[eb] {
			return order[ea] < order[eb]
		}
		return g.Issues[ea].Title < g.Issues[eb].Title
	})
}

func (g *Graph) prioLess(a, b string) bool {
	if g.Issues[a].Priority != g.Issues[b].Priority {
		return g.Issues[a].Priority < g.Issues[b].Priority
	}
	return g.Issues[a].Title < g.Issues[b].Title
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
