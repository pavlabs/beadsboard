package ui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	"github.com/pavlabs/beadsboard/internal/agentreg"
	"github.com/pavlabs/beadsboard/internal/beads"
	"github.com/pavlabs/beadsboard/internal/dispatch"
)

type uiReadyStub struct{ issues []beads.Issue }

func (s *uiReadyStub) Ready(_ context.Context) ([]beads.Issue, error) { return s.issues, nil }

type uiRegistryStub struct{}

func (uiRegistryStub) List() ([]agentreg.Record, error) { return nil, nil }

func dispatchTestModel(ready *uiReadyStub) model {
	m := testModel()
	m.dispatchRun = dispatch.NewCampaign(dispatch.New(ready, uiRegistryStub{}))
	m.startDispatch([]string{"a.1", "a.2"}, agentreg.ToolClaude)
	return m
}

func batchCommands(t *testing.T, cmd tea.Cmd) tea.BatchMsg {
	t.Helper()
	msg, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	return msg
}

func TestAgentEventReevaluatesActiveDispatch(t *testing.T) {
	m := dispatchTestModel(&uiReadyStub{})

	_, cmd := m.Update(agentEventMsg{})
	// testModel has no registry reader, so the batch contains the re-armed agent
	// event wait and dispatch readiness refresh.
	require.Len(t, batchCommands(t, cmd), 2)
}

func TestAdoptedStatusChangeReevaluatesActiveDispatch(t *testing.T) {
	m := dispatchTestModel(&uiReadyStub{})
	m.cfg.GitHubSync = false

	_, cmd := m.adopt(m.graph, 99)
	require.NotNil(t, cmd)
	msg := cmd().(dispatchReadyMsg)
	require.NoError(t, msg.err)
}

func TestDispatchReadyLaunchesEveryAdmittedTask(t *testing.T) {
	m := dispatchTestModel(&uiReadyStub{})

	_, cmd := m.Update(dispatchReadyMsg{issues: []beads.Issue{{ID: "a.1"}, {ID: "a.2"}}})
	require.Len(t, batchCommands(t, cmd), 2)
}
