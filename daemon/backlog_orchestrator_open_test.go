package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sachiniyer/agent-factory/agentproto"
	"github.com/sachiniyer/agent-factory/backlog"
	"github.com/sachiniyer/agent-factory/session"
)

func openOrch(m *Manager, repoID string, id int) (backlog.OpenOrchestratorResponse, error) {
	return m.OpenBacklogOrchestrator(backlog.OpenOrchestratorRequest{RepoID: repoID, ID: id})
}

func stubOrchCreate(t *testing.T) *[]CreateSessionRequest {
	t.Helper()
	var creates []CreateSessionRequest
	prev := createBacklogSession
	createBacklogSession = func(_ *Manager, _ context.Context, req CreateSessionRequest) (session.InstanceData, error) {
		creates = append(creates, req)
		return session.InstanceData{ID: "n1", Title: "effort B-2 Effort"}, nil
	}
	t.Cleanup(func() { createBacklogSession = prev })
	return &creates
}

func TestOpenOrchestratorNamesALiveSession(t *testing.T) {
	m, repoID := orchestratedGroup(t)
	creates := stubOrchCreate(t)

	out, err := openOrch(m, repoID, 2)
	require.NoError(t, err)
	require.Equal(t, backlog.OpenOrchestratorResponse{SessionID: "o1", Title: "planner"}, out)
	require.Empty(t, *creates)
	require.Len(t, orchOf(t, repoID).Events, 1, "opening sends no events")
}

func TestOpenOrchestratorRestoresAnArchivedSession(t *testing.T) {
	m, repoID := orchestratedGroup(t)
	sent := stubBacklogSend(t, m, repoID)
	prevNeeds, prevRestore := backlogNeedsRestore, restoreBacklogSession
	backlogNeedsRestore = func(s itemSession) bool { return s.instance.ID == "o1" }
	var restored []string
	restoreBacklogSession = func(_ *Manager, req RestoreSessionRequest) (session.InstanceData, error) {
		restored = append(restored, req.ID)
		return session.InstanceData{ID: req.ID}, nil
	}
	t.Cleanup(func() { backlogNeedsRestore, restoreBacklogSession = prevNeeds, prevRestore })

	out, err := openOrch(m, repoID, 2)
	require.NoError(t, err)
	require.Equal(t, backlog.OrchOpenRestored, out.Change)
	require.Equal(t, "o1", out.SessionID)
	require.Equal(t, []string{"o1"}, restored)
	require.Equal(t, 1, sent.count())
	require.Contains(t, sent.reqs[0].Prompt, "restarted without its earlier conversation")
	require.NotContains(t, sent.reqs[0].Prompt, "Events in", "events wait for the next delivery")
}

func TestOpenOrchestratorReplacesAGoneSession(t *testing.T) {
	m, repoID := orchestratedGroup(t)
	m.mu.Lock()
	delete(m.instances, daemonInstanceKey(repoID, "planner"))
	m.mu.Unlock()
	creates := stubOrchCreate(t)
	sub, ch, _ := m.events.subscribeWithOverflow()
	defer m.events.unsubscribe(sub)

	out, err := openOrch(m, repoID, 2)
	require.NoError(t, err)
	require.Equal(t, backlog.OpenOrchestratorResponse{SessionID: "n1", Title: "effort B-2 Effort", Change: backlog.OrchOpenReplaced}, out)
	require.Len(t, *creates, 1)
	c := (*creates)[0]
	require.Equal(t, "claude", c.Program)
	require.False(t, c.OrchestratorTurn, "the user opens it, so its turn's end does not archive it")
	require.Contains(t, c.Prompt, "af started this session to replace it")
	o := orchOf(t, repoID)
	require.Equal(t, "n1", o.SessionID)
	require.Len(t, o.Events, 1, "pending events stay for the next delivery")
	evs := orchestratorEvents(ch)
	require.Len(t, evs, 1)
	require.Equal(t, agentproto.EventBacklogOrchestratorReplaced, evs[0].Type)
}

func TestOpenOrchestratorStartsOneForAnUnboundGroup(t *testing.T) {
	m, repoID := orchestratedGroup(t)
	_, err := m.backlogService().SetOrchestrator(backlog.SetOrchestratorRequest{RepoID: repoID, ID: 2, Clear: true})
	require.NoError(t, err)
	creates := stubOrchCreate(t)
	sub, ch, _ := m.events.subscribeWithOverflow()
	defer m.events.unsubscribe(sub)

	out, err := openOrch(m, repoID, 2)
	require.NoError(t, err)
	require.Equal(t, backlog.OrchOpenStarted, out.Change)
	require.Len(t, *creates, 1)
	require.Contains(t, (*creates)[0].Prompt, "started this session from the web")
	require.Equal(t, "n1", orchOf(t, repoID).SessionID)
	evs := orchestratorEvents(ch)
	require.Len(t, evs, 1)
	require.Equal(t, agentproto.EventBacklogOrchestratorBound, evs[0].Type)
}

func TestOpenOrchestratorRefusesAnItemThatIsNotAGroup(t *testing.T) {
	m, repoID := orchestratedGroup(t)
	creates := stubOrchCreate(t)

	_, err := openOrch(m, repoID, 3)
	require.ErrorContains(t, err, "not a top-level parent")
	require.Empty(t, *creates)
}

func TestOpenOrchestratorRefusedDuringADelivery(t *testing.T) {
	m, repoID := orchestratedGroup(t)
	require.True(t, m.backlog.beginOrchestrate(repoID, 2))
	defer m.backlog.endOrchestrate(repoID, 2)

	_, err := openOrch(m, repoID, 2)
	require.ErrorContains(t, err, "try again in a moment")
}
