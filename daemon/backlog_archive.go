package daemon

import (
	"errors"
	"fmt"

	"github.com/sachiniyer/agent-factory/backlog"
	"github.com/sachiniyer/agent-factory/session"
)

// archiveBacklogSession archives an idle item session for reason
// (backlog.ArchiveDone or backlog.ArchiveAsk), or an idle orchestrator for
// backlog.ArchiveOrchIdle, while that reason holds. The
// archive is filed on the session first, so a restart finishes it. While a
// client is attached, or the session is not idle yet, it waits for a later
// poll of the session. When the guard refuses, the session stays live and the
// item gets a note.
func (m *Manager) archiveBacklogSession(repoID, sessionID, reason string) {
	b, err := backlog.Load(repoID)
	if err != nil {
		m.warn().Printf("backlog archive (%s) of session %s: %v", reason, sessionID, err)
		return
	}
	s, ok := m.projectSession(repoID, sessionID)
	if !ok {
		return
	}
	inst := s.instance
	if !b.ArchiveOwed(sessionID, reason) || s.view.Liveness == session.LiveArchived {
		m.settleOwedArchive(repoID, inst)
		return
	}
	m.fileOwedArchive(repoID, inst, reason)
	if m.isPollPaused(repoID, inst.Title, sessionID) || inst.GetLiveness() != session.LiveReady {
		m.backlog.deferArchive(sessionID, reason)
		return
	}
	guard := backlogArchiveGuard(sessionID)
	if err := guard(inst); err != nil {
		m.keepBacklogSessionLive(repoID, inst, reason, err)
		return
	}
	var refused error
	fenced := func(current *session.Instance) error {
		if refused = guard(current); refused != nil {
			return refused
		}
		if b, err := backlog.Load(repoID); err != nil || !b.ArchiveOwed(sessionID, reason) {
			refused = errors.New("the archive is no longer owed")
		}
		return refused
	}
	if err := archiveSessionForLifecycle(m, ArchiveSessionRequest{ID: sessionID, RepoID: repoID}, fenced); err != nil {
		if refused != nil {
			m.keepBacklogSessionLive(repoID, inst, reason, refused)
			return
		}
		m.warn().Printf("backlog archive (%s) of session %q: %v", reason, inst.Title, err)
		return
	}
	inst.SetOwedArchive(nil)
}

// keepBacklogSessionLive settles a refused archive: the session stays, and the
// item gets a note with why, unless the archive is no longer owed (an answer
// came first). There is no retry: a change means someone is using the session.
func (m *Manager) keepBacklogSessionLive(repoID string, inst *session.Instance, reason string, why error) {
	m.info().Printf("backlog archive: keeping session %q: %v", inst.Title, why)
	m.settleOwedArchive(repoID, inst)
	svc := m.backlogService()
	if _, err := svc.Apply(backlog.ProjectRef{ID: repoID}, func(b *backlog.Backlog) ([]int, error) {
		id, err := b.KeptLive(inst.ID, reason, why.Error(), svc.Now())
		if err != nil || id == 0 {
			return nil, err
		}
		return []int{id}, nil
	}); err != nil {
		m.warn().Printf("backlog note on session %q: %v", inst.Title, err)
	}
}

func (m *Manager) fileOwedArchive(repoID string, inst *session.Instance, reason string) {
	owed := inst.OwedArchive()
	if owed != nil && owed.Reason == reason {
		return
	}
	filed := &session.OwedArchiveData{Reason: reason, FiledAt: nowFunc()}
	if owed != nil {
		filed.FiledAt = owed.FiledAt
	}
	inst.SetOwedArchive(filed)
	if err := m.persistAndPublishInstanceErr(repoID, inst); err != nil {
		m.warn().Printf("the owed archive of session %q could not be written to disk (a restart before the archive would forget it): %v",
			inst.Title, err)
	}
}

func (m *Manager) settleOwedArchive(repoID string, inst *session.Instance) {
	if inst.OwedArchive() == nil {
		return
	}
	inst.SetOwedArchive(nil)
	m.persistAndPublishInstance(repoID, inst)
}

// backlogArchiveGuard refuses an item-session archive unless the session is
// still the same idle item session with nothing done in it since its run
// ended: no new run, no delivery, and no pane change. The delivery counters
// are in memory, so for an archive af filed before a restart it also refuses
// pane churn or a run end after the filing.
func backlogArchiveGuard(sessionID string) sessionTeardownGuard {
	return func(current *session.Instance) error {
		switch {
		case current == nil || current.ID != sessionID:
			return fmt.Errorf("session %s was replaced", sessionID)
		case current.BacklogRunActive():
			return errors.New("a new run started")
		case current.GetLiveness() != session.LiveReady:
			return errors.New("the session is not idle")
		case current.AdoptionDeliveries() != current.AdoptionDeliveriesAtRunEnd():
			return errors.New("a prompt reached the session after its run ended")
		case current.LastPaneChurnAt().After(current.BacklogRunEndedAt()):
			return errors.New("the session changed after its run ended")
		}
		if owed := current.OwedArchive(); owed != nil {
			switch {
			case current.LastPaneChurnAt().After(owed.FiledAt):
				return errors.New("the session changed after af planned the archive")
			case current.BacklogRunEndedAt().After(owed.FiledAt):
				return errors.New("a new run ended after af planned the archive")
			}
		}
		return nil
	}
}

// drainBacklogDeferredArchive retries a parked archive once the session is
// polled normally again and idle.
func (m *Manager) drainBacklogDeferredArchive(repoID string, instance *session.Instance) {
	reason, ok := m.backlog.takeDeferredArchive(instance.ID)
	if !ok {
		return
	}
	if instance.GetLiveness() != session.LiveReady {
		m.backlog.deferArchive(instance.ID, reason)
		return
	}
	sessionID := instance.ID
	m.launchBackgroundMutation(func(<-chan struct{}) {
		m.archiveBacklogSession(repoID, sessionID, reason)
	})
}

// onBacklogItemDone applies on_done archive when the user marks an item done:
// now if its session is idle; at the run's idle edge if a run is active.
func (m *Manager) onBacklogItemDone(repoID string, it backlog.Item) {
	if it.Status != backlog.StatusDone || len(it.Runs) == 0 {
		return
	}
	r := it.Runs[len(it.Runs)-1]
	s, ok := m.itemSessions(repoID)[r.SessionID]
	if r.State != backlog.RunStarted || !ok || s.instance.BacklogRunActive() {
		return
	}
	sessionID := r.SessionID
	m.launchBackgroundMutation(func(<-chan struct{}) {
		m.archiveBacklogSession(repoID, sessionID, backlog.ArchiveDone)
	})
}

// redriveOwedArchives finishes the archives an earlier daemon filed and did
// not settle, skipping the sessions whose run end recovery already applied.
func (m *Manager) redriveOwedArchives(repoID string, sessions map[string]itemSession, skip map[string]bool) {
	for id, s := range sessions {
		if skip[id] {
			continue
		}
		if owed := s.instance.OwedArchive(); owed != nil {
			m.archiveBacklogSession(repoID, id, owed.Reason)
		}
	}
}

// redriveOrchestratorArchives finishes the idle archives of orchestrators an
// earlier daemon filed and did not settle.
func (m *Manager) redriveOrchestratorArchives(repoID string, b *backlog.Backlog) {
	for _, it := range b.Items {
		if it.Orchestrator == nil {
			continue
		}
		s, ok := m.projectSession(repoID, it.Orchestrator.SessionID)
		if !ok || s.instance.BacklogItem != 0 {
			continue
		}
		if owed := s.instance.OwedArchive(); owed != nil {
			m.archiveBacklogSession(repoID, s.instance.ID, owed.Reason)
		}
	}
}
