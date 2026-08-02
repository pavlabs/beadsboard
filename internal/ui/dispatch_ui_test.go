package ui

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pavlabs/beadsboard/internal/agentreg"
	"github.com/pavlabs/beadsboard/internal/beads"
	"github.com/pavlabs/beadsboard/internal/dispatch"
)

func campaignModel() model {
	m := testModel()
	m.dispatchRun = dispatch.NewCampaign(dispatch.New(m.client, m.reg))
	return m
}

func TestAutoRunOpensBackendPickerAndStartsEpicCampaign(t *testing.T) {
	m := campaignModel()

	next, _ := m.handleKey(keyMsg("D"))
	m = next.(model)
	require.True(t, m.pickerOpen)
	require.Equal(t, "subtree", m.pickerScope)
	require.Contains(t, m.pickerView(80, 20), "AUTO-RUN")
	require.NotContains(t, m.pickerView(80, 20), "planning")

	next, cmd := m.handleKey(keyMsg("o"))
	m = next.(model)
	require.NotNil(t, cmd)
	require.False(t, m.pickerOpen)
	require.Equal(t, agentreg.ToolCodex, m.dispatchTool)
	require.Equal(t, map[string]bool{"a.2": true}, m.dispatchRun.Pending(), "closed tasks are not queued")
}

func TestAutoRunReportsEpicWithNoOpenTasks(t *testing.T) {
	m := campaignModel()
	is := m.graph.Issues["a.2"]
	is.Status = "closed"
	m.graph.Issues["a.2"] = is
	m.graph = beads.BuildGraph(m.graph.Issues)
	m.openPicker("a", "subtree")

	next, cmd := m.handleKey(keyMsg("l"))
	m = next.(model)
	require.Nil(t, cmd)
	require.Equal(t, "no open tasks to auto-run", m.notice)
}

func TestCampaignTaskRowsShowQueuedBlockedAndRunning(t *testing.T) {
	m := campaignModel()
	issues := m.graph.Issues
	issues["a.3"] = beads.Issue{ID: "a.3", Title: "ship", IssueType: "task", Status: "open", Dependencies: []beads.Dep{
		{DependsOnID: "a", Type: "parent-child"},
		{DependsOnID: "a.2", Type: "blocks"},
	}}
	m.graph = beads.BuildGraph(issues)
	m.startDispatch([]string{"a.2", "a.3"}, agentreg.ToolClaude)

	out := m.taskListContent(70, 12)
	require.Contains(t, out, "queued")
	require.Contains(t, out, "blocked: waits #2")

	m.agentRecords = []agentreg.Record{{ID: "worker", BeadID: "a.2", PID: os.Getpid()}}
	m.agentAlive = map[string]bool{"worker": true}
	out = m.taskListContent(70, 12)
	require.Contains(t, out, "running")
}

func TestFooterAdvertisesAutoRun(t *testing.T) {
	require.Contains(t, testModel().footerLine(), "D auto-run")
}
