package beads

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// statusUpdateArgs sets the new status, adds its carrier label, and removes the
// carrier label of every other status so exactly one remains.
func TestStatusUpdateArgs(t *testing.T) {
	got := statusUpdateArgs("bd-1", "in_progress", []string{"open", "in_progress", "closed"})
	require.Equal(t, []string{
		"update", "bd-1",
		"--status", "in_progress",
		"--add-label", "status:in_progress",
		"--remove-label", "status:open",
		"--remove-label", "status:closed",
	}, got)
}
