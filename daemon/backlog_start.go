package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sachiniyer/agent-factory/backlog"
	"github.com/sachiniyer/agent-factory/proposal"
	"github.com/sachiniyer/agent-factory/session"
)

// createBacklogSession is the create an item start performs. A package var so
// tests can stand in for a real session create.
var createBacklogSession = func(m *Manager, ctx context.Context, req CreateSessionRequest) (session.InstanceData, error) {
	return m.CreateSession(ctx, req)
}

// sendBacklogPrompt sends "continue" to an item's live session.
var sendBacklogPrompt = func(m *Manager, req SendPromptRequest) error {
	_, err := m.SendPromptWithStatus(req)
	return err
}

func (s *controlServer) StartBacklogItem(req StartBacklogItemRequest, resp *StartBacklogItemResponse) error {
	if err := s.requireStateMutationAdmission(); err != nil {
		return err
	}
	run, err := s.manager.StartBacklogItem(req, backlog.StartedByUser)
	if err != nil {
		return err
	}
	resp.Run = run
	return nil
}

// StartBacklogItem starts item req.ID now. An item with a live session gets
// "continue" in that session; any other item gets a new session, created on a
// tracked goroutine, and the returned run is still starting.
func (m *Manager) StartBacklogItem(req backlog.StartRequest, by string) (backlog.RunRef, error) {
	svc := m.backlogService()
	ref, err := svc.Resolve(req.RepoID, req.RepoPath)
	if err != nil {
		return backlog.RunRef{}, err
	}
	b, err := backlog.Load(ref.ID)
	if err != nil {
		return backlog.RunRef{}, err
	}
	facts := backlogHost{m: m}.Facts(ref.ID)
	plan, err := b.BeginStart(req.ID, by, facts, svc.Now())
	if err != nil {
		return backlog.RunRef{}, svc.Named(ref, err)
	}
	if plan.Continue != "" {
		return m.continueBacklogItem(ref, req.ID, plan.Continue)
	}
	root := req.RepoPath
	if root == "" {
		if root, err = registeredProjectRootForRepoID(ref.ID); err != nil {
			return backlog.RunRef{}, err
		}
		if root == "" {
			return backlog.RunRef{}, fmt.Errorf("project %s is not registered; open it in af first", ref.ID)
		}
	}

	var create CreateSessionRequest
	m.backlog.reserve(ref.ID)
	_, err = svc.Apply(ref, func(b *backlog.Backlog) ([]int, error) {
		p, err := b.BeginStart(req.ID, by, facts, svc.Now())
		if err != nil {
			return nil, err
		}
		if p.Continue != "" {
			return nil, fmt.Errorf("%s changed while starting; retry", backlog.FormatID(req.ID))
		}
		plan = p
		it := b.Item(req.ID)
		if it.Program != "" {
			if err := validateCreateProgram(it.Program); err != nil {
				return nil, err
			}
		}
		create = CreateSessionRequest{
			TitleBase:   backlog.SessionTitleBase(it),
			RepoPath:    root,
			Program:     it.Program,
			Prompt:      b.ItemPrompt(it, proposalDir(ref.ID, it)),
			Backend:     string(session.BackendLocal),
			BacklogItem: it.ID,
		}
		return []int{req.ID}, nil
	})
	if err != nil {
		m.backlog.release(ref.ID)
		return backlog.RunRef{}, svc.Named(ref, err)
	}
	launched := m.launchBackgroundMutation(func(stop <-chan struct{}) {
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
		m.finishBacklogStart(ref, req.ID, plan.Run.StartedAt, data, err)
	})
	if !launched {
		m.finishBacklogStart(ref, req.ID, plan.Run.StartedAt, session.InstanceData{}, errors.New("af is shutting down"))
	}
	return plan.Run, nil
}

func proposalDir(repoID string, it *backlog.Item) string {
	if it.Source == nil || it.Source.Proposal == "" {
		return ""
	}
	store, err := proposal.Open(repoID)
	if err != nil {
		return ""
	}
	return store.SlugPath(it.Source.Proposal)
}

// finishBacklogStart records a create's outcome on its starting run, then
// releases the slot the create held. The write is retried, since a run left
// starting holds the item until the next daemon start.
func (m *Manager) finishBacklogStart(ref backlog.ProjectRef, id int, startedAt time.Time, data session.InstanceData, createErr error) {
	defer m.backlog.release(ref.ID)
	if createErr == nil && data.ID == "" {
		createErr = errors.New("the daemon created no session")
	}
	svc := m.backlogService()
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		_, err = svc.Apply(ref, func(b *backlog.Backlog) ([]int, error) {
			return []int{id}, b.FinishStart(id, startedAt, data.ID, data.Title, createErr, svc.Now())
		})
		if err == nil || !errors.Is(err, backlog.ErrBusy) {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		m.warn().Printf("record start of %s: %v", backlog.FormatID(id), err)
	}
}

// continueBacklogItem re-arms the item's live session and sends it "continue".
// The marker is armed before the send so the turn it starts is the run; a send
// that fails disarms it and leaves the item as it was.
func (m *Manager) continueBacklogItem(ref backlog.ProjectRef, id int, sessionID string) (backlog.RunRef, error) {
	svc := m.backlogService()
	s, ok := m.itemSessions(ref.ID)[sessionID]
	if !ok {
		return backlog.RunRef{}, fmt.Errorf("the session for %s is gone; retry \"af backlog start %d\" to start a new one", backlog.FormatID(id), id)
	}
	inst := s.instance
	if !inst.ArmBacklogRun(time.Now()) {
		return backlog.RunRef{}, fmt.Errorf("session %q is not an item session", inst.Title)
	}
	m.persistAndPublishInstance(ref.ID, inst)
	b, err := backlog.Load(ref.ID)
	if err != nil {
		m.disarmBacklogRun(ref.ID, inst)
		return backlog.RunRef{}, err
	}
	it := b.Item(id)
	if it == nil {
		m.disarmBacklogRun(ref.ID, inst)
		return backlog.RunRef{}, svc.Named(ref, &backlog.NotFoundError{ID: id})
	}
	if err := sendBacklogPrompt(m, SendPromptRequest{ID: inst.ID, Title: inst.Title, RepoID: ref.ID, Prompt: backlog.ContinuePrompt(it)}); err != nil {
		m.disarmBacklogRun(ref.ID, inst)
		return backlog.RunRef{}, fmt.Errorf("send continue to session %q: %w", inst.Title, err)
	}
	out, err := svc.Apply(ref, func(b *backlog.Backlog) ([]int, error) {
		return []int{id}, b.Continued(id, sessionID, svc.Now())
	})
	if err != nil {
		return backlog.RunRef{}, svc.Named(ref, err)
	}
	runs := out.Item(id).Runs
	return runs[len(runs)-1], nil
}

func (m *Manager) disarmBacklogRun(repoID string, inst *session.Instance) {
	if inst.DisarmBacklogRun() {
		m.persistAndPublishInstance(repoID, inst)
	}
}
