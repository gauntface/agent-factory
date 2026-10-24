package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sachiniyer/agent-factory/backlog"
)

func (h backlogHost) StartAnswerRun(repoID string, req backlog.AnswerRunRequest) (backlog.Message, error) {
	return h.m.startAnswerRun(backlog.ProjectRef{ID: repoID}, req)
}

// startAnswerRun answers a question whose session is gone with a new run of
// the item. The answer and the starting run are written together; the session
// is created on a tracked goroutine, like any start.
func (m *Manager) startAnswerRun(ref backlog.ProjectRef, req backlog.AnswerRunRequest) (backlog.Message, error) {
	svc := m.backlogService()
	b, err := backlog.Load(ref.ID)
	if err != nil {
		return backlog.Message{}, err
	}
	prevSession, prev := "", backlog.PrevRun{}
	if last := b.LatestRun(req.ID); last != nil {
		prevSession, prev.Branch = last.SessionID, last.Branch
		if s, ok := m.itemSessions(ref.ID)[last.SessionID]; ok && prev.Branch == "" {
			prev.Branch = s.instance.GetBranch()
		}
	}
	if _, err := b.BeginAnswerRun(req.ID, req.QuestionID, req.Text, svc.Now()); err != nil {
		return backlog.Message{}, err
	}
	root, err := backlogProjectRoot(ref.ID, "")
	if err != nil {
		return backlog.Message{}, err
	}
	if prev.Branch != "" {
		prev.SHA, prev.Checked = remoteBranchSHA(root, prev.Branch)
	}

	var (
		create CreateSessionRequest
		out    backlog.AnswerRun
	)
	m.backlog.reserve(ref.ID)
	_, err = svc.Apply(ref, func(b *backlog.Backlog) ([]int, error) {
		if last := b.LatestRun(req.ID); last != nil && last.SessionID != prevSession {
			return nil, fmt.Errorf("%s changed while answering; retry", backlog.FormatID(req.ID))
		}
		var err error
		if out, err = b.BeginAnswerRun(req.ID, req.QuestionID, req.Text, svc.Now()); err != nil {
			return nil, err
		}
		it := b.Item(req.ID)
		prev.PR = it.PR
		prompt := b.AnswerRunPrompt(it, proposalDir(ref.ID, it), prev, out.Question, out.Answered.Answer.Text)
		create, err = backlogCreateRequest(it, root, prompt)
		return []int{req.ID}, err
	})
	if err != nil {
		m.backlog.release(ref.ID)
		return backlog.Message{}, err
	}
	m.launchBacklogCreate(ref, req.ID, out.Plan.Run.StartedAt, create)
	return out.Answered.Answer, nil
}

const remoteBranchTimeout = 5 * time.Second

// remoteBranchSHA asks origin for branch's last commit. ok is false when af
// could not ask. A package var so tests need no remote.
var remoteBranchSHA = func(root, branch string) (sha string, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), remoteBranchTimeout)
	defer cancel()
	ref := "refs/heads/" + branch
	cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-remote", "origin", ref)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.WaitDelay = time.Second
	stdout, err := cmd.Output()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(stdout), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == ref {
			return f[0], true
		}
	}
	return "", true
}
