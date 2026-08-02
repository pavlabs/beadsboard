package dispatch

import (
	"context"
	"os"
	"testing"

	"github.com/pavlabs/beadsboard/internal/agentreg"
	"github.com/pavlabs/beadsboard/internal/beads"
	"github.com/stretchr/testify/require"
)

type readyStub struct {
	issues []beads.Issue
	err    error
}

func (s readyStub) Ready(context.Context) ([]beads.Issue, error) { return s.issues, s.err }

type registryStub struct {
	records []agentreg.Record
	err     error
}

func (s registryStub) List() ([]agentreg.Record, error) { return s.records, s.err }

func TestAdmitRespectsCapacityAndExistingAgent(t *testing.T) {
	ready := readyStub{issues: []beads.Issue{{ID: "busy"}, {ID: "next"}, {ID: "last"}}}
	reg := registryStub{records: []agentreg.Record{{ID: "running", BeadID: "busy", PID: os.Getpid()}}}
	got, err := New(ready, reg).Admit(context.Background(), 2)
	require.NoError(t, err)
	require.Equal(t, []beads.Issue{{ID: "next"}}, got)
}

func TestAdmitIgnoresDeadRecordsAndDeduplicates(t *testing.T) {
	ready := readyStub{issues: []beads.Issue{{ID: "one"}, {ID: "one"}, {ID: "two"}}}
	reg := registryStub{records: []agentreg.Record{{ID: "dead", BeadID: "one", PID: 99999999}}}
	got, err := New(ready, reg).Admit(context.Background(), 0)
	require.NoError(t, err)
	require.Equal(t, []beads.Issue{{ID: "one"}, {ID: "two"}}, got)
}

func TestAdmitStopsWhenCapacityIsFull(t *testing.T) {
	ready := readyStub{issues: []beads.Issue{{ID: "one"}}}
	reg := registryStub{records: []agentreg.Record{{ID: "running", PID: os.Getpid()}}}
	got, err := New(ready, reg).Admit(context.Background(), 1)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestAdmitSubsetFiltersBeforeFillingCapacity(t *testing.T) {
	ready := readyStub{issues: []beads.Issue{{ID: "unrelated"}, {ID: "epic-task"}}}
	got, err := New(ready, registryStub{}).AdmitSubset(context.Background(), 1, map[string]bool{"epic-task": true})
	require.NoError(t, err)
	require.Equal(t, []beads.Issue{{ID: "epic-task"}}, got)
}

type changingReady struct{ issues []beads.Issue }

func (s *changingReady) Ready(context.Context) ([]beads.Issue, error) { return s.issues, nil }

func TestCampaignReevaluatesNewlyUnblockedTasksWithoutRedispatch(t *testing.T) {
	ready := &changingReady{issues: []beads.Issue{{ID: "first"}}}
	campaign := NewCampaign(New(ready, registryStub{}))
	campaign.Start([]string{"first", "dependent"})

	got, err := campaign.Reevaluate(context.Background(), 2)
	require.NoError(t, err)
	require.Equal(t, []beads.Issue{{ID: "first"}}, got)
	require.Equal(t, map[string]bool{"dependent": true}, campaign.Pending())

	// A completion/status change makes the dependent ready. The first task may
	// still appear in bd ready briefly, but a campaign admits every task once.
	ready.issues = []beads.Issue{{ID: "first"}, {ID: "dependent"}}
	got, err = campaign.Reevaluate(context.Background(), 2)
	require.NoError(t, err)
	require.Equal(t, []beads.Issue{{ID: "dependent"}}, got)
	require.Empty(t, campaign.Pending())
	require.False(t, campaign.NeedsEvaluation())
}
