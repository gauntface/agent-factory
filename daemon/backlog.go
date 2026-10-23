package daemon

import (
	"path/filepath"
	"sync"

	"github.com/sachiniyer/agent-factory/agentproto"
	"github.com/sachiniyer/agent-factory/backlog"
)

// routeServices holds the services behind the proposal and backlog routes;
// proposals also runs the poll-time doc scan.
type routeServices struct {
	proposals proposalState
	backlog   backlogState
}

// backlogState holds the manager's backlog service. It is created on first
// use, so a Manager built as a literal in tests needs no setup.
type backlogState struct {
	once sync.Once
	svc  *backlog.Service
	// slots counts the item-session creates in flight per project. In memory
	// only: a restart has none, and its starting runs are recovered from records.
	mu          sync.Mutex
	slots       map[string]int
	recoverOnce sync.Once
}

func (s *backlogState) reserve(repoID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.slots == nil {
		s.slots = map[string]int{}
	}
	s.slots[repoID]++
}

func (s *backlogState) release(repoID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.slots[repoID] <= 1 {
		delete(s.slots, repoID)
		return
	}
	s.slots[repoID]--
}

func (s *backlogState) reserved(repoID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.slots[repoID]
}

func (m *Manager) backlogService() *backlog.Service {
	m.backlog.once.Do(func() {
		m.backlog.svc = backlog.NewService(backlogHost{m: m})
	})
	return m.backlog.svc
}

// backlogHost is the Manager seen through backlog.Host.
type backlogHost struct {
	m *Manager
}

func (h backlogHost) Facts(repoID string) backlog.Facts {
	sessions := h.m.itemSessions(repoID)
	return func(id string) backlog.SessionFacts {
		s, ok := sessions[id]
		return backlogSessionFacts(s, ok)
	}
}

func (h backlogHost) SlotsUsed(repoID string) int { return h.m.backlogSlotsUsed(repoID) }

func (h backlogHost) ProjectName(repoID string) string {
	if root, err := registeredProjectRootForRepoID(repoID); err == nil && root != "" {
		return filepath.Base(root)
	}
	h.m.mu.Lock()
	defer h.m.mu.Unlock()
	for key, instance := range h.m.instances {
		if rid, _ := splitDaemonInstanceKey(key); rid == repoID {
			if path := instance.GetRepoPath(); path != "" {
				return filepath.Base(path)
			}
		}
	}
	return ""
}

func (h backlogHost) Changed(repoID string, ids []int) {
	if ids == nil {
		ids = []int{}
	}
	h.m.publishEvent(agentproto.EventBacklogChanged, backlog.ChangedEvent{RepoID: repoID, IDs: ids})
}

func (s *controlServer) GetBacklog(req GetBacklogRequest, resp *GetBacklogResponse) error {
	if err := s.requireManagerReady(); err != nil {
		return err
	}
	out, err := s.manager.backlogService().Get(req)
	if err != nil {
		return err
	}
	*resp = *out
	return nil
}

func (s *controlServer) AddBacklogItems(req AddBacklogItemsRequest, resp *AddBacklogItemsResponse) error {
	if err := s.requireStateMutationAdmission(); err != nil {
		return err
	}
	out, err := s.manager.backlogService().Add(req)
	if err != nil {
		return err
	}
	*resp = *out
	return nil
}

func (s *controlServer) UpdateBacklogItem(req UpdateBacklogItemRequest, resp *UpdateBacklogItemResponse) error {
	if err := s.requireStateMutationAdmission(); err != nil {
		return err
	}
	out, err := s.manager.backlogService().UpdateItem(req)
	if err != nil {
		return err
	}
	*resp = *out
	return nil
}

func (s *controlServer) MoveBacklogItem(req MoveBacklogItemRequest, resp *MoveBacklogItemResponse) error {
	if err := s.requireStateMutationAdmission(); err != nil {
		return err
	}
	return s.manager.backlogService().Move(req)
}

func (s *controlServer) SetBacklogItemStatus(req SetBacklogItemStatusRequest, resp *SetBacklogItemStatusResponse) error {
	if err := s.requireStateMutationAdmission(); err != nil {
		return err
	}
	out, err := s.manager.backlogService().SetItemStatus(req)
	if err != nil {
		return err
	}
	*resp = *out
	return nil
}

func (s *controlServer) DeleteBacklogItem(req DeleteBacklogItemRequest, resp *DeleteBacklogItemResponse) error {
	if err := s.requireStateMutationAdmission(); err != nil {
		return err
	}
	return s.manager.backlogService().DeleteItem(req)
}

func (s *controlServer) PostBacklogMessage(req PostBacklogMessageRequest, resp *PostBacklogMessageResponse) error {
	if err := s.requireStateMutationAdmission(); err != nil {
		return err
	}
	out, err := s.manager.backlogService().PostMessage(req)
	if err != nil {
		return err
	}
	*resp = *out
	return nil
}

func (s *controlServer) SetBacklogSettings(req SetBacklogSettingsRequest, resp *SetBacklogSettingsResponse) error {
	if err := s.requireStateMutationAdmission(); err != nil {
		return err
	}
	out, err := s.manager.backlogService().SetSettings(req)
	if err != nil {
		return err
	}
	*resp = *out
	return nil
}

func (s *controlServer) GetBudget(req GetBudgetRequest, resp *GetBudgetResponse) error {
	if err := s.requireManagerReady(); err != nil {
		return err
	}
	out, err := s.manager.backlogService().GetBudget(req)
	if err != nil {
		return err
	}
	*resp = *out
	return nil
}

func (s *controlServer) SetBudget(req SetBudgetRequest, resp *SetBudgetResponse) error {
	if err := s.requireStateMutationAdmission(); err != nil {
		return err
	}
	out, err := s.manager.backlogService().SetBudget(req)
	if err != nil {
		return err
	}
	*resp = *out
	return nil
}
