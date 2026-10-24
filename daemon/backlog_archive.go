package daemon

import (
	"errors"
	"fmt"

	"github.com/sachiniyer/agent-factory/backlog"
	"github.com/sachiniyer/agent-factory/session"
)

// archiveItemSession archives an idle item session for reason
// (backlog.ArchiveDone or backlog.ArchiveAsk) while that reason holds. The
// archive is filed on the session first, so a restart finishes it. While a
// client is attached it waits for the next unpaused poll of the session. When
// the guard refuses, the session stays live and the item gets a note.
func (m *Manager) archiveItemSession(repoID, sessionID, reason string) {
	b, err := backlog.Load(repoID)
	if err != nil {
		m.warn().Printf("backlog archive (%s) of session %s: %v", reason, sessionID, err)
		return
	}
	s, ok := m.itemSessions(repoID)[sessionID]
	if !ok {
		return
	}
	inst := s.instance
	if !b.ArchiveOwed(sessionID, reason) || s.view.Liveness == session.LiveArchived {
		m.settleOwedArchive(repoID, inst)
		return
	}
	m.fileOwedArchive(repoID, inst, reason)
	if m.isPollPaused(repoID, inst.Title, sessionID) {
		m.backlog.deferArchive(sessionID, reason)
		return
	}
	guard := backlogArchiveGuard(sessionID)
	if err := guard(inst); err != nil {
		m.keepItemSessionLive(repoID, inst, err)
		return
	}
	var refused error
	fenced := func(current *session.Instance) error {
		refused = guard(current)
		return refused
	}
	if err := archiveSessionForLifecycle(m, ArchiveSessionRequest{ID: sessionID, RepoID: repoID}, fenced); err != nil {
		if refused != nil {
			m.keepItemSessionLive(repoID, inst, refused)
			return
		}
		m.warn().Printf("backlog archive (%s) of session %q: %v", reason, inst.Title, err)
		return
	}
	inst.SetOwedArchive(nil)
}

// keepItemSessionLive settles a refused archive: the session stays, and the
// item gets a note with why. There is no retry: a change means someone is
// using the session.
func (m *Manager) keepItemSessionLive(repoID string, inst *session.Instance, why error) {
	m.info().Printf("backlog archive: keeping session %q: %v", inst.Title, why)
	m.settleOwedArchive(repoID, inst)
	svc := m.backlogService()
	if _, err := svc.Apply(backlog.ProjectRef{ID: repoID}, func(b *backlog.Backlog) ([]int, error) {
		id, err := b.KeptLive(inst.ID, why.Error(), svc.Now())
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

// drainBacklogDeferredArchive retries an archive parked while a client was
// attached, now that the session is polled normally again.
func (m *Manager) drainBacklogDeferredArchive(repoID string, instance *session.Instance) {
	if instance.BacklogItem == 0 {
		return
	}
	reason, ok := m.backlog.takeDeferredArchive(instance.ID)
	if !ok {
		return
	}
	sessionID := instance.ID
	m.launchBackgroundMutation(func(<-chan struct{}) {
		m.archiveItemSession(repoID, sessionID, reason)
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
		m.archiveItemSession(repoID, sessionID, backlog.ArchiveDone)
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
			m.archiveItemSession(repoID, id, owed.Reason)
		}
	}
}
