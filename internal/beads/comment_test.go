package beads

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// A comment array from `bd comments --json` decodes into the timeline fields the
// UI renders, oldest first.
func TestCommentDecode(t *testing.T) {
	raw := `[
	  {"id":"c1","issue_id":"bd-1","author":"art","text":"first","created_at":"2026-07-22T18:54:54Z"},
	  {"id":"c2","issue_id":"bd-1","author":"art","text":"bb-agent spawn agent=bd-1-1","created_at":"2026-07-22T18:55:00Z"}
	]`
	var got []Comment
	require.NoError(t, json.Unmarshal([]byte(raw), &got))
	require.Len(t, got, 2)
	require.Equal(t, "bd-1", got[0].IssueID)
	require.Equal(t, "art", got[0].Author)
	require.Equal(t, "first", got[0].Text)
	require.Equal(t, "2026-07-22T18:54:54Z", got[0].CreatedAt)
	require.Equal(t, "bb-agent spawn agent=bd-1-1", got[1].Text)
}
