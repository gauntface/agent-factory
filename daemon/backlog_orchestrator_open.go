package daemon

import (
	"context"
	"errors"
	"fmt"

	"github.com/sachiniyer/agent-factory/agentproto"
	"github.com/sachiniyer/agent-factory/backlog"
	"github.com/sachiniyer/agent-factory/session"
)

func (s *controlServer) OpenBacklogOrchestrator(req OpenBacklogOrchestratorRequest, resp *OpenBacklogOrchestratorResponse) error {
	if err := s.requireStateMutationAdmission(); err != nil {
		return err
	}
	out, err := s.manager.OpenBacklogOrchestrator(req)
	if err != nil {
		return err
	}
	*resp = out
	return nil
}

type orchOpenResult struct {
	out backlog.OpenOrchestratorResponse
	err error
}

// OpenBacklogOrchestrator names the session to open for group req.ID: its
// orchestrator, restored first when archived, lost or dead; a replacement
// bound in its place when it is gone; or a new session bound to a group with
// none. It holds the group's delivery claim, so no delivery races it.
func (m *Manager) OpenBacklogOrchestrator(req backlog.OpenOrchestratorRequest) (backlog.OpenOrchestratorResponse, error) {
	svc := m.backlogService()
	ref, err := svc.Resolve(req.RepoID, req.RepoPath)
	if err != nil {
		return backlog.OpenOrchestratorResponse{}, err
	}
	if !m.backlog.beginOrchestrate(ref.ID, req.ID) {
		return backlog.OpenOrchestratorResponse{}, fmt.Errorf("af is sending events to the effort session of %s; try again in a moment", backlog.FormatID(req.ID))
	}
	done := make(chan orchOpenResult, 1)
	if !m.launchBackgroundMutation(func(stop <-chan struct{}) {
		defer m.backlog.endOrchestrate(ref.ID, req.ID)
		out, err := m.openOrchestrator(stop, ref, req)
		done <- orchOpenResult{out, svc.Named(ref, err)}
	}) {
		m.backlog.endOrchestrate(ref.ID, req.ID)
		return backlog.OpenOrchestratorResponse{}, errors.New("af is shutting down")
	}
	r := <-done
	return r.out, r.err
}

func (m *Manager) openOrchestrator(stop <-chan struct{}, ref backlog.ProjectRef, req backlog.OpenOrchestratorRequest) (backlog.OpenOrchestratorResponse, error) {
	var none backlog.OpenOrchestratorResponse
	b, err := backlog.Load(ref.ID)
	if err != nil {
		return none, err
	}
	o, err := b.OrchestratorToOpen(req.ID)
	if err != nil {
		return none, err
	}
	if o == nil {
		return m.startOrchestrator(stop, ref, b, req.ID, nil)
	}
	s, ok := m.projectSession(ref.ID, o.SessionID)
	if ok && !s.view.UserKilled && s.view.InFlightOp != session.OpKilling {
		return m.restoreOrchestrator(ref.ID, b, req.ID, s)
	}
	if !m.orchestratorReady(ref.ID) {
		return none, errors.New("af is still loading this project's sessions; try again in a moment")
	}
	return m.startOrchestrator(stop, ref, b, req.ID, o)
}

// restoreOrchestrator restores the group's orchestrator when it needs it. A
// session whose agent conversation is gone gets the orchestrator's first
// prompt again.
func (m *Manager) restoreOrchestrator(repoID string, b *backlog.Backlog, group int, s itemSession) (backlog.OpenOrchestratorResponse, error) {
	out := backlog.OpenOrchestratorResponse{SessionID: s.instance.ID, Title: s.instance.Title}
	if !backlogNeedsRestore(s) {
		return out, nil
	}
	fresh := !s.instance.AgentConversation().HasID()
	restored, err := restoreBacklogSession(m, RestoreSessionRequest{ID: s.instance.ID, RepoID: repoID})
	if err != nil {
		return backlog.OpenOrchestratorResponse{}, fmt.Errorf("af could not restore effort session %q: %w", s.instance.Title, err)
	}
	m.publishEvent(agentproto.EventSessionRestored, restored)
	out.Change = backlog.OrchOpenRestored
	if !fresh {
		return out, nil
	}
	if s, ok := m.projectSession(repoID, out.SessionID); ok {
		prompt := b.OrchStartPrompt(group, proposalDir(repoID, b.Item(group)), backlog.OrchRestarted, nil)
		if err := m.sendBacklogAnswer(repoID, s.instance, prompt); err != nil {
			m.warn().Printf("send the first prompt to restored effort session %q: %v", s.instance.Title, err)
		}
	}
	return out, nil
}

// startOrchestrator starts a session for group and binds it: in place of
// prev, or as the group's first orchestrator when prev is nil. The group's
// pending events stay queued for the next delivery.
func (m *Manager) startOrchestrator(stop <-chan struct{}, ref backlog.ProjectRef, b *backlog.Backlog, group int, prev *backlog.Orchestrator) (backlog.OpenOrchestratorResponse, error) {
	var none backlog.OpenOrchestratorResponse
	g := b.Item(group)
	why, program := backlog.OrchNew, g.Program
	if prev != nil {
		why, program = backlog.OrchReplacement, prev.Program
	}
	root, err := backlogProjectRoot(ref.ID, "")
	if err == nil && program != "" {
		err = validateCreateProgram(program)
	}
	if err != nil {
		return none, fmt.Errorf("af could not start an effort session: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	data, err := createBacklogSession(m, ctx, CreateSessionRequest{
		TitleBase: orchestratorTitleBase(g),
		RepoPath:  root,
		Program:   program,
		Prompt:    b.OrchStartPrompt(group, proposalDir(ref.ID, g), why, nil),
		Backend:   string(session.BackendLocal),
	})
	if err == nil && data.ID == "" {
		err = errors.New("the daemon created no session")
	}
	if err != nil {
		return none, fmt.Errorf("af could not start an effort session: %w", err)
	}
	next := backlog.Orchestrator{SessionID: data.ID, Title: data.Title, Program: program}
	svc := m.backlogService()
	_, err = svc.Apply(ref, func(b *backlog.Backlog) ([]int, error) {
		if prev != nil {
			return []int{group}, b.OrchReplaced(group, prev.SessionID, next, nil, svc.Now())
		}
		return []int{group}, b.BindNewOrchestrator(group, next, svc.Now())
	})
	if err != nil {
		if killed, kerr := killOrchestratorSession(m, KillSessionRequest{ID: data.ID, Title: data.Title, RepoID: ref.ID}); kerr != nil {
			m.warn().Printf("kill unbound effort session %q: %v", data.Title, kerr)
		} else {
			m.publishEvent(agentproto.EventSessionKilled, killed)
		}
		return none, err
	}
	ev := backlog.OrchestratorEvent{RepoID: ref.ID, ID: group, SessionID: data.ID, Title: data.Title}
	out := backlog.OpenOrchestratorResponse{SessionID: data.ID, Title: data.Title, Change: backlog.OrchOpenStarted}
	t := agentproto.EventBacklogOrchestratorBound
	if prev != nil {
		ev.Previous, out.Change, t = prev.SessionID, backlog.OrchOpenReplaced, agentproto.EventBacklogOrchestratorReplaced
	}
	m.publishEvent(t, ev)
	return out, nil
}
