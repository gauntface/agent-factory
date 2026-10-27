package backlog

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// What a host reports of a session's state.
const (
	SessionLive     = "live"
	SessionArchived = "archived"
	SessionLost     = "lost"
	SessionDead     = "dead"
	SessionLimited  = "limited"
	// SessionGone: the session was killed or never existed.
	SessionGone = "gone"
)

// SessionInfo is what the host knows of one session of a project. Item is the
// backlog item the session was started for, or 0.
type SessionInfo struct {
	Title   string
	Program string
	Item    int
	State   string
}

// ErrOrchestrateFromItemSession refuses an item session as an orchestrator.
var ErrOrchestrateFromItemSession = errors.New("run this from the session that should orchestrate, not from an item session")

type SetOrchestratorRequest struct {
	RepoID    string `json:"repo_id,omitempty"`
	RepoPath  string `json:"repo_path,omitempty"`
	ID        int    `json:"id"`
	SessionID string `json:"session_id,omitempty"`
	// Clear unbinds the group; it cannot go with SessionID.
	Clear bool `json:"clear,omitempty"`
}

// OrchestratorResponse is a group and its orchestrator; Orchestrator is nil
// when the group has none.
type OrchestratorResponse struct {
	ID           int               `json:"id"`
	Orchestrator *OrchestratorView `json:"orchestrator"`
}

type SetOrchestratorHandoffRequest struct {
	RepoID    string `json:"repo_id,omitempty"`
	RepoPath  string `json:"repo_path,omitempty"`
	SessionID string `json:"session_id"`
	Handoff   string `json:"handoff"`
}

// OrchestratorView is a group's orchestrator as the routes show it: the
// binding with the session's state and the count of undelivered events.
type OrchestratorView struct {
	SessionID string    `json:"session_id"`
	Title     string    `json:"title"`
	Program   string    `json:"program,omitempty"`
	BoundAt   time.Time `json:"bound_at"`
	State     string    `json:"state"`
	Pending   int       `json:"pending"`
	Handoff   string    `json:"handoff,omitempty"`
}

// OrchestratorBoundEvent is the payload of a backlog.orchestrator.bound
// event.
type OrchestratorBoundEvent struct {
	RepoID    string `json:"repo_id"`
	ID        int    `json:"id"`
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
}

func (s *Service) orchestratorView(repoID string, o *Orchestrator) *OrchestratorView {
	if o == nil {
		return nil
	}
	state := SessionGone
	if info, ok := s.host.Session(repoID, o.SessionID); ok && info.State != "" {
		state = info.State
	}
	return &OrchestratorView{SessionID: o.SessionID, Title: o.Title, Program: o.Program, BoundAt: o.BoundAt,
		State: state, Pending: len(o.Events), Handoff: o.Handoff}
}

func (s *Service) orchestratorViews(repoID string, b *Backlog) map[int]OrchestratorView {
	var out map[int]OrchestratorView
	for _, it := range b.Items {
		if it.Orchestrator == nil {
			continue
		}
		if out == nil {
			out = map[int]OrchestratorView{}
		}
		out[it.ID] = *s.orchestratorView(repoID, it.Orchestrator)
	}
	return out
}

// newOrchestrator checks that sessionID is a session of the project that may
// orchestrate, and returns its binding.
func (s *Service) newOrchestrator(p *project, sessionID string) (Orchestrator, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return Orchestrator{}, errors.New("session_id names the session to bind")
	}
	info, ok := s.host.Session(p.id, sessionID)
	if !ok || info.State == SessionGone {
		return Orchestrator{}, fmt.Errorf("session %q is not a session of this project", sessionID)
	}
	if info.Item != 0 {
		return Orchestrator{}, ErrOrchestrateFromItemSession
	}
	return Orchestrator{SessionID: sessionID, Title: info.Title, Program: info.Program}, nil
}

func (s *Service) bind(b *Backlog, id int, o Orchestrator) error {
	if b.ItemForSession(o.SessionID) != nil {
		return ErrOrchestrateFromItemSession
	}
	return b.BindOrchestrator(id, o, s.now())
}

// SetOrchestrator binds a session to a group as its orchestrator, or with
// Clear unbinds it.
func (s *Service) SetOrchestrator(req SetOrchestratorRequest) (*OrchestratorResponse, error) {
	p, err := s.resolve(req.RepoID, req.RepoPath)
	if err != nil {
		return nil, err
	}
	var o Orchestrator
	switch {
	case req.Clear && req.SessionID != "":
		return nil, errors.New("clear unbinds the group; it cannot go with session_id")
	case !req.Clear:
		if o, err = s.newOrchestrator(p, req.SessionID); err != nil {
			return nil, err
		}
	}
	b, err := s.update(p, func(b *Backlog) ([]int, error) {
		if req.Clear {
			_, err := b.ClearOrchestrator(req.ID, s.now())
			return []int{req.ID}, err
		}
		return []int{req.ID}, s.bind(b, req.ID, o)
	})
	if err != nil {
		return nil, err
	}
	return &OrchestratorResponse{ID: req.ID, Orchestrator: s.orchestratorView(p.id, b.Item(req.ID).Orchestrator)}, nil
}

// SetOrchestratorHandoff stores the notes for a session that replaces the
// orchestrator.
func (s *Service) SetOrchestratorHandoff(req SetOrchestratorHandoffRequest) (*OrchestratorResponse, error) {
	p, err := s.resolve(req.RepoID, req.RepoPath)
	if err != nil {
		return nil, err
	}
	var id int
	b, err := s.update(p, func(b *Backlog) ([]int, error) {
		var err error
		id, err = b.SetOrchestratorHandoff(req.SessionID, req.Handoff, s.now())
		return []int{id}, err
	})
	if err != nil {
		return nil, err
	}
	return &OrchestratorResponse{ID: id, Orchestrator: s.orchestratorView(p.id, b.Item(id).Orchestrator)}, nil
}

// batchGroup is the one top-level item the batch's items sit under.
func batchGroup(b *Backlog, ids map[string]int) (int, error) {
	roots := map[int]bool{}
	var order []int
	for _, it := range b.Items {
		for _, id := range ids {
			if it.ID != id {
				continue
			}
			if r := b.rootOf(id); !roots[r] {
				roots[r] = true
				order = append(order, r)
			}
		}
	}
	if len(order) != 1 {
		names := make([]string, len(order))
		for i, r := range order {
			names[i] = FormatID(r)
		}
		return 0, fmt.Errorf("the batch is under %d top-level items (%s); orchestrate needs it under one group", len(order), strings.Join(names, ", "))
	}
	return order[0], nil
}
