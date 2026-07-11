package beads

// Issue is a beads issue as emitted by `bd export --all` (one JSON object per
// line, fully populated with labels and dependencies).
type Issue struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Status       string   `json:"status"` // open | in_progress | closed
	Priority     int      `json:"priority"`
	IssueType    string   `json:"issue_type"` // epic | task
	Description  string   `json:"description"`
	Notes        string   `json:"notes"`
	Labels       []string `json:"labels"`
	Dependencies []Dep    `json:"dependencies"`
	UpdatedAt    string   `json:"updated_at"`
	ExternalRef  string   `json:"external_ref"` // cross-system link, e.g. "gh-42"; set once synced
}

// Dep is one dependency edge: this issue depends on DependsOnID. Type is
// "blocks" (DependsOnID must close first) or "parent-child" (DependsOnID is
// this issue's epic).
type Dep struct {
	DependsOnID string `json:"depends_on_id"`
	Type        string `json:"type"`
}

func (i Issue) IsEpic() bool { return i.IssueType == "epic" }

// parentID returns the epic this issue is a child of, via its parent-child
// edge, or "" if it has none.
func (i Issue) parentID() string {
	for _, d := range i.Dependencies {
		if d.Type == "parent-child" {
			return d.DependsOnID
		}
	}
	return ""
}

// blockers returns the ids this issue is blocked by (open or not).
func (i Issue) blockers() []string {
	var out []string
	for _, d := range i.Dependencies {
		if d.Type == "blocks" {
			out = append(out, d.DependsOnID)
		}
	}
	return out
}
