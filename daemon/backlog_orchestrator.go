package daemon

import (
	"github.com/sachiniyer/agent-factory/agentproto"
	"github.com/sachiniyer/agent-factory/backlog"
	"github.com/sachiniyer/agent-factory/session"
)

func (h backlogHost) Session(repoID, sessionID string) (backlog.SessionInfo, bool) {
	h.m.mu.Lock()
	var inst *session.Instance
	for key, i := range h.m.instances {
		if i == nil || i.ID != sessionID {
			continue
		}
		if rid, _ := splitDaemonInstanceKey(key); rid == repoID {
			inst = i
			break
		}
	}
	if inst == nil {
		h.m.mu.Unlock()
		return backlog.SessionInfo{}, false
	}
	info := backlog.SessionInfo{Title: inst.Title, Program: inst.Program, Item: inst.BacklogItem}
	v := inst.LifecycleView()
	h.m.mu.Unlock()
	info.State = backlogSessionState(v)
	return info, true
}

func backlogSessionState(v session.LifecycleView) string {
	if v.UserKilled || v.InFlightOp == session.OpKilling {
		return backlog.SessionGone
	}
	switch v.Liveness {
	case session.LiveArchived:
		return backlog.SessionArchived
	case session.LiveDead:
		return backlog.SessionDead
	case session.LiveLost:
		return backlog.SessionLost
	case session.LiveLimitReached:
		return backlog.SessionLimited
	}
	return backlog.SessionLive
}

func (m *Manager) publishOrchestratorBound(repoID string, out *backlog.OrchestratorResponse) {
	if out == nil || out.Orchestrator == nil {
		return
	}
	m.publishEvent(agentproto.EventBacklogOrchestratorBound, backlog.OrchestratorBoundEvent{
		RepoID: repoID, ID: out.ID, SessionID: out.Orchestrator.SessionID, Title: out.Orchestrator.Title})
}

func (s *controlServer) SetBacklogOrchestrator(req SetBacklogOrchestratorRequest, resp *SetBacklogOrchestratorResponse) error {
	if err := s.requireStateMutationAdmission(); err != nil {
		return err
	}
	svc := s.manager.backlogService()
	out, err := svc.SetOrchestrator(req)
	if err != nil {
		return err
	}
	*resp = *out
	if !req.Clear {
		if ref, err := svc.Resolve(req.RepoID, req.RepoPath); err == nil {
			s.manager.publishOrchestratorBound(ref.ID, out)
		}
	}
	return nil
}

func (s *controlServer) SetBacklogOrchestratorHandoff(req SetBacklogOrchestratorHandoffRequest, resp *SetBacklogOrchestratorHandoffResponse) error {
	if err := s.requireStateMutationAdmission(); err != nil {
		return err
	}
	out, err := s.manager.backlogService().SetOrchestratorHandoff(req)
	if err != nil {
		return err
	}
	*resp = *out
	return nil
}
