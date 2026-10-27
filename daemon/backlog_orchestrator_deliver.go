package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/sachiniyer/agent-factory/agentproto"
	"github.com/sachiniyer/agent-factory/backlog"
	"github.com/sachiniyer/agent-factory/session"
)

// deliverOrchestrators starts a delivery for each group of the project whose
// orchestrator has events due. A project waits until restart recovery has
// seen its sessions, so a session not loaded yet is not taken for gone.
func (m *Manager) deliverOrchestrators(repoID string, b *backlog.Backlog, now time.Time) {
	if !m.backlog.isRecovered(repoID) {
		return
	}
	for _, d := range b.OrchDeliveries(now) {
		group := d.Group
		if !m.backlog.beginOrchestrate(repoID, group) {
			continue
		}
		if !m.launchBackgroundMutation(func(stop <-chan struct{}) {
			defer m.backlog.endOrchestrate(repoID, group)
			m.deliverOrchestrator(stop, repoID, group, now)
		}) {
			m.backlog.endOrchestrate(repoID, group)
		}
	}
}

// deliverOrchestrator sends group's pending events to its orchestrator in one
// prompt. A live session gets the prompt; an archived, lost or dead one is
// restored first; a killed or missing one is replaced by a new session whose
// first prompt carries the handoff and the events. A client attached or a
// usage limit holds the send for a later pass. Any other failure counts
// toward escalation.
func (m *Manager) deliverOrchestrator(stop <-chan struct{}, repoID string, group int, now time.Time) {
	b, err := backlog.Load(repoID)
	if err != nil {
		m.warn().Printf("orchestrator delivery for %s: %v", backlog.FormatID(group), err)
		return
	}
	var d backlog.OrchDelivery
	for _, c := range b.OrchDeliveries(now) {
		if c.Group == group {
			d = c
		}
	}
	if d.Group == 0 {
		return
	}
	s, ok := m.projectSession(repoID, d.SessionID)
	if !ok || s.view.UserKilled || s.view.InFlightOp == session.OpKilling {
		m.replaceOrchestrator(stop, repoID, b, d)
		return
	}
	prompt := b.OrchEventsPrompt(group, d.Events)
	if backlogNeedsRestore(s) {
		if !s.instance.AgentConversation().HasID() {
			prompt = b.OrchStartPrompt(group, proposalDir(repoID, b.Item(group)), false, d.Events)
		}
		restored, err := restoreBacklogSession(m, RestoreSessionRequest{ID: d.SessionID, RepoID: repoID})
		if err != nil {
			m.orchDeliveryFailed(repoID, d, "af could not restore the session: "+err.Error())
			return
		}
		m.publishEvent(agentproto.EventSessionRestored, restored)
		if s, ok = m.projectSession(repoID, d.SessionID); !ok {
			m.orchDeliveryFailed(repoID, d, "the session is gone")
			return
		}
	}
	err = m.sendBacklogAnswer(repoID, s.instance, prompt)
	switch {
	case err == nil:
		m.orchDelivered(repoID, d)
	case errors.Is(err, errBacklogAnswerHeld), errors.Is(err, session.ErrAdoptionFenced):
	default:
		m.orchDeliveryFailed(repoID, d, err.Error())
	}
}

// orchestratorTitleBase names a replacement orchestrator session.
func orchestratorTitleBase(g *backlog.Item) string { return "effort " + backlog.SessionTitleBase(g) }

// replaceOrchestrator starts a new session for group, with the group's
// program, and binds it in place of the session that is gone.
func (m *Manager) replaceOrchestrator(stop <-chan struct{}, repoID string, b *backlog.Backlog, d backlog.OrchDelivery) {
	g := b.Item(d.Group)
	root, err := backlogProjectRoot(repoID, "")
	if err == nil && d.Program != "" {
		err = validateCreateProgram(d.Program)
	}
	if err != nil {
		m.orchDeliveryFailed(repoID, d, "af could not start a replacement session: "+err.Error())
		return
	}
	create := CreateSessionRequest{
		TitleBase: orchestratorTitleBase(g),
		RepoPath:  root,
		Program:   d.Program,
		Prompt:    b.OrchStartPrompt(d.Group, proposalDir(repoID, g), true, d.Events),
		Backend:   string(session.BackendLocal),
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
	data, err := createBacklogSession(m, ctx, create)
	if err == nil && data.ID == "" {
		err = errors.New("the daemon created no session")
	}
	if err != nil {
		m.orchDeliveryFailed(repoID, d, "af could not start a replacement session: "+err.Error())
		return
	}
	next := backlog.Orchestrator{SessionID: data.ID, Title: data.Title, Program: d.Program}
	svc := m.backlogService()
	if _, err := svc.Apply(backlog.ProjectRef{ID: repoID}, func(b *backlog.Backlog) ([]int, error) {
		return []int{d.Group}, b.OrchReplaced(d.Group, d.SessionID, next, d.Events, svc.Now())
	}); err != nil {
		m.warn().Printf("bind replacement orchestrator %q to %s: %v", data.Title, backlog.FormatID(d.Group), err)
		return
	}
	m.publishEvent(agentproto.EventBacklogOrchestratorReplaced, backlog.OrchestratorEvent{RepoID: repoID, ID: d.Group,
		SessionID: data.ID, Title: data.Title, Previous: d.SessionID})
}

func (m *Manager) orchDelivered(repoID string, d backlog.OrchDelivery) {
	svc := m.backlogService()
	if _, err := svc.Apply(backlog.ProjectRef{ID: repoID}, func(b *backlog.Backlog) ([]int, error) {
		if !b.OrchDelivered(d.Group, d.SessionID, d.Events, svc.Now()) {
			return nil, errBacklogNoChange
		}
		return []int{d.Group}, nil
	}); err != nil && !errors.Is(err, errBacklogNoChange) {
		m.warn().Printf("record orchestrator delivery on %s: %v", backlog.FormatID(d.Group), err)
	}
}

func (m *Manager) orchDeliveryFailed(repoID string, d backlog.OrchDelivery, reason string) {
	svc := m.backlogService()
	if _, err := svc.Apply(backlog.ProjectRef{ID: repoID}, func(b *backlog.Backlog) ([]int, error) {
		b.OrchDeliveryFailed(d.Group, d.SessionID, reason, svc.Now())
		return []int{d.Group}, nil
	}); err != nil {
		m.warn().Printf("record failed orchestrator delivery on %s: %v", backlog.FormatID(d.Group), err)
	}
}
