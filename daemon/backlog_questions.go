package daemon

import (
	"time"

	"github.com/sachiniyer/agent-factory/agentproto"
	"github.com/sachiniyer/agent-factory/backlog"
	"github.com/sachiniyer/agent-factory/session"
)

// restoreBacklogSession restores an archived, lost or dead item session before
// an answer is delivered. A package var so tests can stand in for a restore.
var restoreBacklogSession = func(m *Manager, req RestoreSessionRequest) (session.InstanceData, error) {
	_, restored, err := m.RestoreSession(req)
	return restored, err
}

// backlogAnswerHoldPoll is how often a held answer checks whether the user
// detached from the session.
var backlogAnswerHoldPoll = 2 * time.Second

func (h backlogHost) SetAttention(repoID, sessionID, reason string) {
	req := SetAttentionRequest{ID: sessionID, RepoID: repoID, Reason: reason, Clear: reason == ""}
	if _, _, err := h.m.SetAttention(req); err != nil {
		h.m.warn().Printf("backlog attention on session %s: %v", sessionID, err)
	}
}

func (h backlogHost) AnswerTarget(repoID, sessionID string) backlog.AnswerTarget {
	s, ok := h.m.itemSessions(repoID)[sessionID]
	if !ok || s.view.UserKilled || s.view.InFlightOp == session.OpKilling {
		return backlog.TargetGone
	}
	switch s.view.Liveness {
	case session.LiveArchived, session.LiveLost, session.LiveDead:
		return backlog.TargetRestore
	}
	return backlog.TargetLive
}

func (h backlogHost) DeliverAnswer(repoID string, id int, sessionID, prompt string, restore bool) {
	m := h.m
	launched := m.launchBackgroundMutation(func(stop <-chan struct{}) {
		m.deliverBacklogAnswer(stop, repoID, id, sessionID, prompt, restore)
	})
	if !launched {
		m.backlogAnswerNotDelivered(repoID, id, "af is shutting down")
	}
}

// deliverBacklogAnswer restores the item session if asked, holds while a client
// is attached full screen, then arms the run and sends the answer.
func (m *Manager) deliverBacklogAnswer(stop <-chan struct{}, repoID string, id int, sessionID, prompt string, restore bool) {
	if restore {
		restored, err := restoreBacklogSession(m, RestoreSessionRequest{ID: sessionID, RepoID: repoID})
		if err != nil {
			m.backlogAnswerNotDelivered(repoID, id, "af could not restore the session: "+err.Error())
			return
		}
		m.publishEvent(agentproto.EventSessionRestored, restored)
	}
	var inst *session.Instance
	for {
		s, ok := m.itemSessions(repoID)[sessionID]
		if !ok {
			m.backlogAnswerNotDelivered(repoID, id, "the session is gone")
			return
		}
		inst = s.instance
		if !m.isPollPaused(repoID, inst.Title, inst.ID) {
			break
		}
		t := time.NewTimer(backlogAnswerHoldPoll)
		select {
		case <-stop:
			t.Stop()
			m.backlogAnswerNotDelivered(repoID, id, "af stopped while the session was attached")
			return
		case <-t.C:
		}
	}
	if !inst.ArmBacklogRun(time.Now()) {
		m.backlogAnswerNotDelivered(repoID, id, "the session is not an item session")
		return
	}
	m.persistAndPublishInstance(repoID, inst)
	if err := sendBacklogPrompt(m, SendPromptRequest{ID: inst.ID, Title: inst.Title, RepoID: repoID, Prompt: prompt}); err != nil {
		m.disarmBacklogRun(repoID, inst)
		m.backlogAnswerNotDelivered(repoID, id, err.Error())
	}
}

func (m *Manager) backlogAnswerNotDelivered(repoID string, id int, reason string) {
	svc := m.backlogService()
	_, err := svc.Apply(backlog.ProjectRef{ID: repoID}, func(b *backlog.Backlog) ([]int, error) {
		_, err := b.AddNote(id, backlog.AuthorAF, "The answer was not delivered: "+reason+". It is kept in the thread.", svc.Now())
		return []int{id}, err
	})
	if err != nil {
		m.warn().Printf("record undelivered answer on %s: %v", backlog.FormatID(id), err)
	}
}

// noteBacklogAnsweredOnRun closes the open question of an item session whose
// agent started work again after it asked and went idle: the user answered in
// the terminal.
func (m *Manager) noteBacklogAnsweredOnRun(repoID string, instance *session.Instance) {
	if instance.TakeAttentionAnswered() && instance.BacklogItem != 0 {
		m.noteBacklogAnsweredInSession(repoID, instance)
	}
}

// noteBacklogAnsweredInSession closes the item's open questions as answered in
// session. A running item gets its run armed again, so the next idle edge ends
// the run as if af had delivered the answer.
func (m *Manager) noteBacklogAnsweredInSession(repoID string, instance *session.Instance) {
	if instance.BacklogItem == 0 {
		return
	}
	sessionID := instance.ID
	m.launchBackgroundMutation(func(<-chan struct{}) {
		svc := m.backlogService()
		running := false
		_, err := svc.Apply(backlog.ProjectRef{ID: repoID}, func(b *backlog.Backlog) ([]int, error) {
			id := b.AnsweredInSession(sessionID, svc.Now())
			if id == 0 {
				return []int{}, nil
			}
			running = b.Item(id).Status == backlog.StatusRunning
			return []int{id}, nil
		})
		if err != nil {
			m.warn().Printf("close question answered in session %s: %v", sessionID, err)
			return
		}
		if running && !instance.BacklogRunActive() && instance.ArmBacklogRun(time.Now()) {
			m.persistAndPublishInstance(repoID, instance)
		}
	})
}
