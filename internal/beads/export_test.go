package beads

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const exportLines = `{"id":"bd-1","title":"one","status":"open","issue_type":"epic"}
{"id":"bd-2","title":"two","status":"open","issue_type":"task"}`

// The revision tracks the issue data, so re-reading unchanged issues yields the
// same value. This is what stops the board reloading on its own `bd` reads: bd
// rewrites Dolt's journal and .beads/last-touched even for a read, so anything
// derived from file state moves when the data has not.
func TestExportRevisionStableAcrossReads(t *testing.T) {
	first, rev1, err := decodeExport(strings.NewReader(exportLines))
	require.NoError(t, err)
	second, rev2, err := decodeExport(strings.NewReader(exportLines))
	require.NoError(t, err)

	require.Equal(t, rev1, rev2)
	require.Len(t, first, 2)
	require.Equal(t, first, second)
	require.NotZero(t, rev1)
}

// A real edit moves the revision, so the watcher still sees external writes.
func TestExportRevisionMovesWithData(t *testing.T) {
	_, rev, err := decodeExport(strings.NewReader(exportLines))
	require.NoError(t, err)
	_, edited, err := decodeExport(strings.NewReader(
		strings.Replace(exportLines, `"status":"open","issue_type":"task"`, `"status":"closed","issue_type":"task"`, 1),
	))
	require.NoError(t, err)

	require.NotEqual(t, rev, edited)
}

// Blank lines are export formatting, not data, and must not move the revision.
func TestExportRevisionIgnoresBlankLines(t *testing.T) {
	_, rev, err := decodeExport(strings.NewReader(exportLines))
	require.NoError(t, err)
	_, padded, err := decodeExport(strings.NewReader("\n" + strings.ReplaceAll(exportLines, "\n", "\n\n") + "\n"))
	require.NoError(t, err)

	require.Equal(t, rev, padded)
}

// A malformed line is an error, not a silently-different revision.
func TestExportRejectsMalformedLine(t *testing.T) {
	_, _, err := decodeExport(strings.NewReader(exportLines + "\nnot json"))
	require.Error(t, err)
}
