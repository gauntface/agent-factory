package daemon

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sachiniyer/agent-factory/backlog"
	"github.com/sachiniyer/agent-factory/config"
	"github.com/sachiniyer/agent-factory/session"
)

// itemSession is one item session of a project, read under one lock.
type itemSession struct {
	instance *session.Instance
	view     session.LifecycleView
	resetAt  string
}

// itemSessions returns the project's item sessions by session id.
func (m *Manager) itemSessions(repoID string) map[string]itemSession {
	out := map[string]itemSession{}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, inst := range m.instances {
		if inst == nil || inst.BacklogItem == 0 {
			continue
		}
		if rid, _ := splitDaemonInstanceKey(key); rid != repoID {
			continue
		}
		s := itemSession{instance: inst, view: inst.LifecycleView()}
		if at, ok := inst.LimitResetAt(); ok && !at.IsZero() {
			s.resetAt = at.Local().Format("15:04")
		}
		out[inst.ID] = s
	}
	return out
}

// backlogSessionFacts is what Derive needs to know about one item session.
// An unknown session was killed: its record is gone.
func backlogSessionFacts(s itemSession, ok bool) backlog.SessionFacts {
	if !ok {
		return backlog.SessionFacts{Reason: "session killed"}
	}
	v := s.view
	switch {
	case v.UserKilled || v.InFlightOp == session.OpKilling:
		return backlog.SessionFacts{Reason: "session killed"}
	case v.StartupStateUnknown:
		return backlog.SessionFacts{Reason: "session did not start"}
	}
	switch v.Liveness {
	case session.LiveArchived:
		return backlog.SessionFacts{Reason: "session archived"}
	case session.LiveDead:
		return backlog.SessionFacts{Reason: "session dead"}
	case session.LiveLost:
		return backlog.SessionFacts{Lost: true, AutoRestore: v.BacklogRunActive && canAutoRestoreLostSession(v), Reason: "session lost"}
	case session.LiveLimitReached:
		reason := "usage limit"
		if s.resetAt != "" {
			reason += " until " + s.resetAt
		}
		return backlog.SessionFacts{Live: true, Reason: reason}
	}
	return backlog.SessionFacts{Live: true, RunActive: v.BacklogRunActive}
}

// holdsBacklogSlot is whether an item session counts against max_running: its
// run is active on a live session, or it is lost mid-run and af will restore
// it. The same facts drive the Working and Stopped states.
func holdsBacklogSlot(s itemSession) bool {
	f := backlogSessionFacts(s, true)
	return f.RunActive || (f.Lost && f.AutoRestore)
}

func (m *Manager) backlogSlotsUsed(repoID string) int {
	used := m.backlog.reserved(repoID)
	for _, s := range m.itemSessions(repoID) {
		if holdsBacklogSlot(s) {
			used++
		}
	}
	return used
}

// noteBacklogRunEnd applies the end of a backlog run when this poll is the idle
// edge that ended it: the marker went true→false and the session settled
// Ready. A teardown that clears the marker lands elsewhere and fires nothing.
// archive is false on the paused path, where an attached client owns the pane.
func (m *Manager) noteBacklogRunEnd(repoID string, instance *session.Instance, wasActive, archive bool) {
	if !wasActive || instance.BacklogItem == 0 || instance.BacklogRunActive() ||
		instance.StartupStateUnknown() || instance.GetLiveness() != session.LiveReady {
		return
	}
	sessionID := instance.ID
	m.launchBackgroundMutation(func(<-chan struct{}) {
		m.backlogRunEnded(repoID, sessionID, archive)
	})
}

// backlogRunEnded moves a silent run's item to review and applies on_done.
func (m *Manager) backlogRunEnded(repoID, sessionID string, archive bool) {
	b, err := backlog.Load(repoID)
	if err != nil {
		m.warn().Printf("backlog run end for session %s: %v", sessionID, err)
		return
	}
	svc := m.backlogService()
	end := b.RunEnded(sessionID, svc.Now())
	if end.Changed {
		ref := backlog.ProjectRef{ID: repoID}
		_, err := svc.Apply(ref, func(b *backlog.Backlog) ([]int, error) {
			if e := b.RunEnded(sessionID, svc.Now()); e.Changed {
				return []int{e.ID}, nil
			}
			return []int{}, nil
		})
		if err != nil {
			m.warn().Printf("backlog run end for session %s: %v", sessionID, err)
		}
	}
	if !end.Archive || !archive {
		return
	}
	guard := func(current *session.Instance) error {
		if current == nil || current.ID != sessionID {
			return fmt.Errorf("session %s was replaced", sessionID)
		}
		if current.BacklogRunActive() {
			return fmt.Errorf("session %s started a new run", sessionID)
		}
		return nil
	}
	if _, _, err := m.archiveSessionGuarded(ArchiveSessionRequest{ID: sessionID, RepoID: repoID}, guard); err != nil {
		m.warn().Printf("backlog on_done archive of session %s: %v", sessionID, err)
	}
}

// RecoverBacklogStarts settles what a daemon restart left behind in every
// project's backlog: runs still starting are matched to the session created
// for them or failed, and a running item whose session went idle while no
// daemon watched goes to review. It runs once per daemon, after instances load.
func (m *Manager) RecoverBacklogStarts() {
	m.backlog.recoverOnce.Do(func() {
		m.launchBackgroundMutation(func(<-chan struct{}) { m.recoverBacklogStarts() })
	})
}

func (m *Manager) recoverBacklogStarts() {
	home, err := config.GetConfigDir()
	if err != nil {
		return
	}
	paths, _ := filepath.Glob(filepath.Join(home, "repos", "*", "backlog.json"))
	for _, p := range paths {
		repoID := filepath.Base(filepath.Dir(p))
		if _, err := os.Stat(p); err != nil {
			continue
		}
		m.recoverBacklog(repoID)
	}
}

func (m *Manager) recoverBacklog(repoID string) {
	b, err := backlog.Load(repoID)
	if err != nil {
		m.warn().Printf("backlog recovery for project %s: %v", repoID, err)
		return
	}
	sessions := m.itemSessions(repoID)
	var list []backlog.ItemSession
	for id, s := range sessions {
		list = append(list, backlog.ItemSession{ID: id, Title: s.instance.Title, Item: s.instance.BacklogItem, CreatedAt: s.instance.CreatedAt})
	}
	ended := idleRunEnds(b, sessions)
	starting := false
	for _, it := range b.Items {
		for _, r := range it.Runs {
			starting = starting || r.State == backlog.RunStarting
		}
	}
	if starting {
		svc := m.backlogService()
		ref := backlog.ProjectRef{ID: repoID}
		if _, err := svc.Apply(ref, func(b *backlog.Backlog) ([]int, error) {
			return b.RecoverStarts(list, svc.Now()), nil
		}); err != nil {
			m.warn().Printf("backlog recovery for project %s: %v", repoID, err)
		}
	}
	for _, sessionID := range ended {
		m.backlogRunEnded(repoID, sessionID, false)
	}
}

// idleRunEnds lists the sessions whose run ended unobserved: the item is still
// running on its latest started run, and the session is live with no run.
func idleRunEnds(b *backlog.Backlog, sessions map[string]itemSession) []string {
	var out []string
	for _, it := range b.Items {
		if it.Status != backlog.StatusRunning || len(it.Runs) == 0 {
			continue
		}
		r := it.Runs[len(it.Runs)-1]
		s, ok := sessions[r.SessionID]
		if r.State != backlog.RunStarted || !ok {
			continue
		}
		f := backlogSessionFacts(s, true)
		if f.Live && !f.RunActive && s.view.Liveness == session.LiveReady {
			out = append(out, r.SessionID)
		}
	}
	return out
}
