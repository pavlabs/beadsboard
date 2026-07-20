package agentreg

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func rec(id, bead string, pid int, started time.Time) Record {
	return Record{
		ID: id, BeadID: bead, Tool: ToolClaude, Mode: ModeCoding,
		PID: pid, Source: SourceBeadsboard, StartedAt: started,
	}
}

// Put/List/ForBead/Remove round-trip, and List is oldest-first.
func TestPutListForBeadRemove(t *testing.T) {
	r, err := New(t.TempDir())
	require.NoError(t, err)

	t0 := time.Now()
	require.NoError(t, r.Put(rec("a-1", "bead-x", 111, t0.Add(2*time.Second))))
	require.NoError(t, r.Put(rec("a-2", "bead-y", 222, t0.Add(1*time.Second))))
	require.NoError(t, r.Put(rec("a-3", "bead-x", 333, t0.Add(3*time.Second))))

	all, err := r.List()
	require.NoError(t, err)
	require.Equal(t, []string{"a-2", "a-1", "a-3"}, ids(all), "oldest first")

	x, err := r.ForBead("bead-x")
	require.NoError(t, err)
	require.Equal(t, []string{"a-1", "a-3"}, ids(x))

	require.NoError(t, r.Remove("a-1"))
	require.NoError(t, r.Remove("a-1"), "removing a missing record is a no-op")
	all, _ = r.List()
	require.Equal(t, []string{"a-2", "a-3"}, ids(all))
}

// Put replaces an existing record in place (e.g. to fill in the session id).
func TestPutOverwrites(t *testing.T) {
	r, err := New(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, r.Put(rec("a-1", "bead-x", 111, time.Now())))

	updated := rec("a-1", "bead-x", 111, time.Now())
	updated.SessionID = "sess-9"
	require.NoError(t, r.Put(updated))

	all, err := r.List()
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Equal(t, "sess-9", all[0].SessionID)
}

func TestPutRejectsBadID(t *testing.T) {
	r, err := New(t.TempDir())
	require.NoError(t, err)
	require.Error(t, r.Put(rec("", "b", 1, time.Now())))
	require.Error(t, r.Put(rec("a/b", "b", 1, time.Now())), "no path separators")
}

// Alive tracks a real process's lifetime; Reap drops the dead ones only.
func TestAliveAndReap(t *testing.T) {
	r, err := New(t.TempDir())
	require.NoError(t, err)

	// A finished process: its PID is dead.
	done := exec.Command("true")
	require.NoError(t, done.Run())
	deadPID := done.Process.Pid

	require.True(t, rec("live", "b", os.Getpid(), time.Now()).Alive(), "own pid is alive")
	require.False(t, rec("dead", "b", deadPID, time.Now()).Alive())
	require.False(t, rec("zero", "b", 0, time.Now()).Alive())

	require.NoError(t, r.Put(rec("live", "b", os.Getpid(), time.Now())))
	require.NoError(t, r.Put(rec("dead", "b", deadPID, time.Now())))

	n, err := r.Reap()
	require.NoError(t, err)
	require.Equal(t, 1, n, "only the dead record reaped")
	all, _ := r.List()
	require.Equal(t, []string{"live"}, ids(all))
}

func ids(recs []Record) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.ID
	}
	return out
}
