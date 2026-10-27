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
// the item. The kept answer and the starting run are written together; the
// session is created on a tracked goroutine, like any start, and the question
// closes when it starts.
func (m *Manager) startAnswerRun(ref backlog.ProjectRef, req backlog.AnswerRunRequest) (backlog.Message, error) {
	svc := m.backlogService()
	b, err := backlog.Load(ref.ID)
	if err != nil {
		return backlog.Message{}, err
	}
	it := b.Item(req.ID)
	if it == nil {
		return backlog.Message{}, &backlog.NotFoundError{ID: req.ID}
	}
	var last backlog.RunRef
	if r := b.LatestRun(req.ID); r != nil {
		last = *r
	}
	prevItem := *it
	if _, err := b.BeginAnswerRun(req.ID, req.QuestionID, req.Text, req.CallerSessionID, svc.Now()); err != nil {
		return backlog.Message{}, err
	}
	root, err := backlogProjectRoot(ref.ID, req.RepoPath)
	if err != nil {
		return backlog.Message{}, err
	}
	prev := m.prevBacklogRun(ref.ID, root, &prevItem)

	var (
		create CreateSessionRequest
		out    backlog.AnswerRun
	)
	m.backlog.reserve(ref.ID)
	_, err = svc.Apply(ref, func(b *backlog.Backlog) ([]int, error) {
		if r := b.LatestRun(req.ID); r != nil && (r.SessionID != last.SessionID || !r.StartedAt.Equal(last.StartedAt)) {
			return nil, fmt.Errorf("%s changed while answering; retry", backlog.FormatID(req.ID))
		}
		var err error
		if out, err = b.BeginAnswerRun(req.ID, req.QuestionID, req.Text, req.CallerSessionID, svc.Now()); err != nil {
			return nil, err
		}
		it := b.Item(req.ID)
		prev.PR = it.PR
		create, err = backlogCreateRequest(it, root, b.StartPrompt(it, proposalDir(ref.ID, it), prev))
		return []int{req.ID}, err
	})
	if err != nil {
		m.backlog.release(ref.ID)
		return backlog.Message{}, err
	}
	m.launchBacklogCreate(ref, req.ID, out.Plan.Run.StartedAt, create)
	return out.Answer, nil
}

// prevBacklogRun is what af knows about the item's latest run that had a
// session: its branch, from the run or else the session record, and that
// branch's last commit on origin.
func (m *Manager) prevBacklogRun(repoID, root string, it *backlog.Item) backlog.PrevRun {
	var prev backlog.PrevRun
	for i := len(it.Runs) - 1; i >= 0; i-- {
		r := it.Runs[i]
		if r.SessionID == "" {
			continue
		}
		prev.Branch = r.Branch
		if s, ok := m.itemSessions(repoID)[r.SessionID]; ok && prev.Branch == "" {
			prev.Branch = s.instance.GetBranch()
		}
		break
	}
	if prev.Branch != "" {
		prev.SHA, prev.Checked = remoteBranchSHA(root, prev.Branch)
	}
	return prev
}

const remoteBranchTimeout = 5 * time.Second

// remoteBranchSHA asks origin for branch's last commit. ok is false when af
// could not ask. A package var so tests need no remote.
var remoteBranchSHA = func(root, branch string) (sha string, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), remoteBranchTimeout)
	defer cancel()
	ref := "refs/heads/" + branch
	cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-remote", "origin", ref)
	// The daemon has no terminal: a credential or host-key prompt must fail
	// rather than wait out the timeout.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=ssh -oBatchMode=yes")
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
