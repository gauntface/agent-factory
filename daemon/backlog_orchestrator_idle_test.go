package daemon

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sachiniyer/agent-factory/backlog"
	"github.com/sachiniyer/agent-factory/config"
	"github.com/sachiniyer/agent-factory/session"
)

// liveOrchestrator puts a started session o1 ("planner") in place of the
// bare one orchestratedGroup binds.
func liveOrchestrator(t *testing.T, m *Manager, repoID string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{ID: "o1", Title: "planner", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	inst.SetBackend(session.NewFakeBackend())
	inst.SetStartedForTest(true)
	m.mu.Lock()
	m.instances[daemonInstanceKey(repoID, "planner")] = inst
	m.mu.Unlock()
	return inst
}

func TestOrchestratorDeliveryOpensATurn(t *testing.T) {
	m, repoID := orchestratedGroup(t)
	inst := liveOrchestrator(t, m, repoID)
	sent := stubBacklogSend(t, m, repoID)

	runOrchDelivery(t, m, repoID, time.Now())

	require.Equal(t, 1, sent.count())
	require.True(t, inst.BacklogRunActive(), "the delivery opens a turn whose end can archive the session")
	require.Zero(t, inst.LifecycleView().BacklogItem)
}

func TestOrchestratorTurnEndArchivesWhenIdle(t *testing.T) {
	m, repoID := orchestratedGroup(t)
	inst := liveOrchestrator(t, m, repoID)
	stubBacklogSend(t, m, repoID)
	archived := stubBacklogArchive(t)
	runOrchDelivery(t, m, repoID, time.Now())
	require.Empty(t, orchOf(t, repoID).Events)

	idleItemSession(t, inst)
	m.orchestratorTurnEnded(repoID, inst.ID)

	require.Equal(t, []string{"o1"}, *archived)
	require.Nil(t, inst.OwedArchive())
}

func TestOrchestratorTurnEndKeepsLiveWithPendingEvents(t *testing.T) {
	m, repoID := orchestratedGroup(t)
	inst := liveOrchestrator(t, m, repoID)
	archived := stubBacklogArchive(t)
	idleItemSession(t, inst)

	m.orchestratorTurnEnded(repoID, inst.ID)

	require.Empty(t, *archived, "the review event still waits for delivery")
}

func TestOrchestratorStayKeepsLiveOnce(t *testing.T) {
	m, repoID := orchestratedGroup(t)
	inst := liveOrchestrator(t, m, repoID)
	stubBacklogSend(t, m, repoID)
	archived := stubBacklogArchive(t)
	runOrchDelivery(t, m, repoID, time.Now())
	_, err := m.backlogService().SetOrchestratorHandoff(backlog.SetOrchestratorHandoffRequest{RepoID: repoID, SessionID: "o1", Stay: true})
	require.NoError(t, err)

	idleItemSession(t, inst)
	m.orchestratorTurnEnded(repoID, inst.ID)
	require.Empty(t, *archived)
	require.False(t, orchOf(t, repoID).Stay, "the turn end uses the stay up")

	m.orchestratorTurnEnded(repoID, inst.ID)
	require.Equal(t, []string{"o1"}, *archived)
}

func TestMeterFindsOrchestrators(t *testing.T) {
	m, repoID := orchestratedGroup(t)
	inst := liveOrchestrator(t, m, repoID)
	home, err := config.GetConfigDir()
	require.NoError(t, err)

	_, orchs := m.startedItemRuns(home)

	require.Len(t, orchs, 1)
	require.Equal(t, repoID, orchs[0].repoID)
	require.Equal(t, 2, orchs[0].group)
	require.Same(t, inst, orchs[0].inst)
	require.Zero(t, m.backlogSlotsUsed(repoID), "an orchestrator takes no max_running slot")
}

func TestOrchestratorWithoutAConversationIsNotMetered(t *testing.T) {
	m, repoID := orchestratedGroup(t)
	inst := liveOrchestrator(t, m, repoID)
	_, _, ok := orchestratorConversation(inst)
	require.False(t, ok, "with no conversation id af cannot tell its transcript from others in the dir")
}

func TestStayRefusedOutsideAnAfTurn(t *testing.T) {
	m, repoID := orchestratedGroup(t)
	liveOrchestrator(t, m, repoID)
	_, err := m.backlogService().SetOrchestratorHandoff(backlog.SetOrchestratorHandoffRequest{RepoID: repoID, SessionID: "o1", Stay: true})
	require.ErrorIs(t, err, backlog.ErrStayNoTurn)
}
