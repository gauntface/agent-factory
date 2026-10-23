package daemon

import (
	"strings"
	"sync"
	"testing"

	"github.com/sachiniyer/agent-factory/backlog"
	"github.com/sachiniyer/agent-factory/session"
)

// askedItem starts item 1, ends its run, and has its agent ask a question.
func askedItem(t *testing.T) (*Manager, string, *session.Instance) {
	t.Helper()
	m, repoID, inst := startedItem(t)
	inst.DisarmBacklogRun()
	if _, err := m.backlogService().PostMessage(backlog.PostMessageRequest{RepoID: repoID, ID: 1,
		Kind: backlog.KindQuestion, Text: "Which db?", SessionID: inst.ID}); err != nil {
		t.Fatal(err)
	}
	return m, repoID, inst
}

type sentPrompts struct {
	mu   sync.Mutex
	reqs []SendPromptRequest
	// paused records whether the session was attached at each send.
	paused []bool
}

func stubBacklogSend(t *testing.T, m *Manager, repoID string) *sentPrompts {
	t.Helper()
	s := &sentPrompts{}
	prev := sendBacklogPrompt
	sendBacklogPrompt = func(_ *Manager, req SendPromptRequest) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.reqs = append(s.reqs, req)
		s.paused = append(s.paused, m.isPollPaused(repoID, req.Title, req.ID))
		return nil
	}
	t.Cleanup(func() { sendBacklogPrompt = prev })
	return s
}

func TestBacklogAskSetsAttention(t *testing.T) {
	m, repoID, inst := askedItem(t)
	if a := inst.Attention(); a == nil || a.Reason != "Which db?" {
		t.Fatalf("attention %+v", a)
	}
	v, err := m.backlogService().Get(backlog.GetRequest{RepoID: repoID})
	if err != nil {
		t.Fatal(err)
	}
	if st := v.States[1].State; st != backlog.StateNeedsYou {
		t.Fatalf("state %s", st)
	}
}

func TestBacklogAnswerDelivers(t *testing.T) {
	m, repoID, inst := askedItem(t)
	sent := stubBacklogSend(t, m, repoID)
	out, err := m.backlogService().PostMessage(backlog.PostMessageRequest{RepoID: repoID, ID: 1, Kind: backlog.KindAnswer, Text: "Postgres"})
	if err != nil {
		t.Fatal(err)
	}
	m.backgroundMutationWG.Wait()
	if out.Message.Kind != backlog.KindAnswer || inst.HasAttention() {
		t.Fatalf("message %+v attention %v", out.Message, inst.HasAttention())
	}
	if len(sent.reqs) != 1 || sent.reqs[0].ID != inst.ID || sent.reqs[0].Prompt != "The user answered your question on B-1: Postgres" {
		t.Fatalf("sent %+v", sent.reqs)
	}
	it := loadItem(t, repoID, 1)
	if it.Status != backlog.StatusRunning || it.OpenQuestion() != nil || !inst.BacklogRunActive() {
		t.Fatalf("item %+v active %v", it, inst.BacklogRunActive())
	}
}

func TestBacklogAnswerHeldWhileAttached(t *testing.T) {
	m, repoID, inst := askedItem(t)
	sent := stubBacklogSend(t, m, repoID)
	prev := backlogAnswerHoldPoll
	backlogAnswerHoldPoll = 0
	t.Cleanup(func() { backlogAnswerHoldPoll = prev })

	m.PauseStatusPoll(repoID, inst.Title, inst.ID)
	if _, err := m.backlogService().PostMessage(backlog.PostMessageRequest{RepoID: repoID, ID: 1, Kind: backlog.KindAnswer, Text: "yes"}); err != nil {
		t.Fatal(err)
	}
	m.ResumeStatusPoll(repoID, inst.Title, inst.ID)
	m.backgroundMutationWG.Wait()
	if len(sent.reqs) != 1 || sent.paused[0] {
		t.Fatalf("sent %+v paused %v", sent.reqs, sent.paused)
	}
}

func TestBacklogAnswerGoneSession(t *testing.T) {
	m, repoID, _ := askedItem(t)
	if _, err := backlog.Update(repoID, func(b *backlog.Backlog) error {
		b.Item(1).Runs[0].SessionID = "gone"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err := m.backlogService().PostMessage(backlog.PostMessageRequest{RepoID: repoID, ID: 1, Kind: backlog.KindAnswer, Text: "x"})
	if err == nil || !strings.HasPrefix(err.Error(), "the session for B-1 is gone") {
		t.Fatal(err)
	}
	it := loadItem(t, repoID, 1)
	if last := it.Thread[len(it.Thread)-1]; last.Kind != backlog.KindAnswer {
		t.Fatalf("answer not kept: %+v", it.Thread)
	}
}

func TestBacklogAnsweredInSessionOnInput(t *testing.T) {
	m, repoID, inst := askedItem(t)
	m.clearAttentionOnInput(inst)
	m.backgroundMutationWG.Wait()
	it := loadItem(t, repoID, 1)
	if it.Thread[0].Closed != backlog.ClosedAnsweredInSession || inst.HasAttention() {
		t.Fatalf("thread %+v attention %v", it.Thread, inst.HasAttention())
	}
	if !inst.BacklogRunActive() {
		t.Fatal("a running item's run is armed again after an answer in session")
	}
}

func TestBacklogDoneClearsAttention(t *testing.T) {
	m, repoID, inst := askedItem(t)
	if _, err := m.backlogService().SetItemStatus(backlog.SetItemStatusRequest{RepoID: repoID, ID: 1, Status: backlog.StatusDone}); err != nil {
		t.Fatal(err)
	}
	if inst.HasAttention() {
		t.Fatal("done left the attention request")
	}
	if c := loadItem(t, repoID, 1).Thread[0].Closed; c != backlog.ClosedDone {
		t.Fatal(c)
	}
}
