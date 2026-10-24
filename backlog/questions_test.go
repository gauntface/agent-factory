package backlog

import (
	"strings"
	"testing"
	"time"
)

// withRun sets item id running in session s1.
func withRun(t *testing.T, id int, status Status) {
	t.Helper()
	if _, err := Update(repoID, func(b *Backlog) error {
		it := b.Item(id)
		it.Status = status
		it.Runs = []RunRef{{SessionID: "s1", Title: "B-1 A", State: RunStarted}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func ask(t *testing.T, s *Service, text string) Message {
	t.Helper()
	out, err := s.PostMessage(PostMessageRequest{RepoID: repoID, ID: 1, Kind: KindQuestion, Text: text, SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	return out.Message
}

// legacyQuestion adds a second open question to item 1, as v1 allowed.
func legacyQuestion(t *testing.T, text string) Message {
	t.Helper()
	var m Message
	if _, err := Update(repoID, func(b *Backlog) error {
		m = *b.Item(1).appendMessage(Message{Kind: KindQuestion, Author: "agent:s1", Text: text}, time.Now())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestServiceAsk(t *testing.T) {
	s, h := newTestService(t)
	ids := mustAdd(t, s, ItemInput{Key: "p", Title: "P"}, ItemInput{Key: "a", Title: "A", Parent: Ref{Key: "p"}})
	withRun(t, ids["a"], StatusRunning)
	if _, err := s.PostMessage(PostMessageRequest{RepoID: repoID, ID: 2, Kind: KindQuestion, Text: "q", SessionID: "s2"}); err == nil {
		t.Error("a question from another session accepted")
	}
	out, err := s.PostMessage(PostMessageRequest{RepoID: repoID, ID: 2, Kind: KindQuestion, Text: " Which db? ", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if m := out.Message; m.Kind != KindQuestion || m.Author != "agent:s1" || m.Text != "Which db?" {
		t.Fatalf("%+v", m)
	}
	if h.attention["s1"] != "Which db?" {
		t.Fatalf("attention %q", h.attention["s1"])
	}
	v, err := s.Get(GetRequest{RepoID: repoID})
	if err != nil {
		t.Fatal(err)
	}
	if v.States[2].State != StateNeedsYou || v.States[1].State != StateNeedsYou {
		t.Fatalf("item %v, parent %v", v.States[2], v.States[1])
	}
}

func TestServiceAskRefusesClosedItem(t *testing.T) {
	s, _ := newTestService(t)
	mustAdd(t, s, ItemInput{Key: "a", Title: "A"})
	withRun(t, 1, StatusDone)
	_, err := s.PostMessage(PostMessageRequest{RepoID: repoID, ID: 1, Kind: KindQuestion, Text: "q", SessionID: "s1"})
	if err == nil || !strings.Contains(err.Error(), "B-1 is done") {
		t.Fatal(err)
	}
}

func TestServiceAnswerDelivers(t *testing.T) {
	s, h := newTestService(t)
	mustAdd(t, s, ItemInput{Key: "a", Title: "A"})
	withRun(t, 1, StatusRunning)
	q := ask(t, s, "Which db?")
	h.target = TargetLive
	out, err := s.PostMessage(PostMessageRequest{RepoID: repoID, ID: 1, Kind: KindAnswer, Text: "Postgres"})
	if err != nil {
		t.Fatal(err)
	}
	if m := out.Message; m.Kind != KindAnswer || m.Author != AuthorUser || m.Answers != q.ID {
		t.Fatalf("%+v", m)
	}
	if h.attention["s1"] != "" {
		t.Fatalf("attention not cleared: %q", h.attention["s1"])
	}
	want := PendingAnswer{ID: 1, AnswerID: out.Message.ID, SessionID: "s1", Prompt: "The user answered your question on B-1: Postgres"}
	if len(h.delivered) != 1 || h.delivered[0] != want {
		t.Fatalf("%+v", h.delivered)
	}
	b, _ := Load(repoID)
	it := b.Item(1)
	if it.Status != StatusRunning || it.OpenQuestion() != nil || it.Thread[0].Closed != ClosedAnswered {
		t.Fatalf("%+v", it)
	}
	if a := it.Thread[1]; a.Delivery != DeliveryPending || a.PrevStatus != StatusRunning {
		t.Fatalf("%+v", a)
	}
	if p := b.PendingAnswers(); len(p) != 1 || p[0] != want {
		t.Fatalf("pending %+v", p)
	}
	_, err = s.PostMessage(PostMessageRequest{RepoID: repoID, ID: 1, Kind: KindAnswer, Text: "again"})
	if err == nil || err.Error() != `B-1 has no open question — use "af backlog note" to add a note` {
		t.Fatal(err)
	}
}

func TestServiceAnswerResumesReviewAndRestores(t *testing.T) {
	s, h := newTestService(t)
	mustAdd(t, s, ItemInput{Key: "a", Title: "A"})
	withRun(t, 1, StatusReview)
	ask(t, s, "Merge it?")
	h.target = TargetRestore
	if _, err := s.PostMessage(PostMessageRequest{RepoID: repoID, ID: 1, Kind: KindAnswer, Text: "yes"}); err != nil {
		t.Fatal(err)
	}
	if len(h.delivered) != 1 {
		t.Fatalf("%+v", h.delivered)
	}
	b, _ := Load(repoID)
	if st := b.Item(1).Status; st != StatusRunning {
		t.Fatal(st)
	}
	if err := b.AnswerFailed(1, h.delivered[0].AnswerID, "no worktree", time.Now()); err != nil {
		t.Fatal(err)
	}
	it := b.Item(1)
	if it.Status != StatusReview || it.Thread[1].Delivery != DeliveryFailed || len(b.PendingAnswers()) != 0 {
		t.Fatalf("%+v", it)
	}
	if note := it.Thread[len(it.Thread)-1]; note.Author != AuthorAF || !strings.Contains(note.Text, "no worktree") {
		t.Fatalf("%+v", note)
	}
}

func TestServiceAnswerGoneStartsRun(t *testing.T) {
	s, h := newTestService(t)
	mustAdd(t, s, ItemInput{Key: "a", Title: "A"})
	withRun(t, 1, StatusRunning)
	ask(t, s, "q")
	h.target = TargetGone
	out, err := s.PostMessage(PostMessageRequest{RepoID: repoID, ID: 1, Kind: KindAnswer, Text: " a "})
	if err != nil {
		t.Fatal(err)
	}
	if out.Message.Kind != KindAnswer || out.Message.Text != "a" {
		t.Fatalf("%+v", out.Message)
	}
	if len(h.delivered) != 0 || len(h.answerRuns) != 1 || h.answerRuns[0] != (AnswerRunRequest{ID: 1, Text: " a "}) {
		t.Fatalf("delivered %+v runs %+v", h.delivered, h.answerRuns)
	}
	b, _ := Load(repoID)
	it := b.Item(1)
	if it.OpenQuestion() != nil || it.Thread[0].Closed != ClosedAnswered || len(it.Runs) != 2 {
		t.Fatalf("%+v", it)
	}
	if r := it.Runs[1]; r.State != RunStarting || r.StartedBy != StartedByUser || it.Status != StatusRunning {
		t.Fatalf("run %+v status %s", r, it.Status)
	}
}

func TestServiceAnswerGoneOnClosedItemKeepsAnswer(t *testing.T) {
	s, h := newTestService(t)
	mustAdd(t, s, ItemInput{Key: "a", Title: "A"})
	withRun(t, 1, StatusRunning)
	ask(t, s, "q")
	if _, err := Update(repoID, func(b *Backlog) error {
		b.Item(1).Status = StatusDone
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h.target = TargetGone
	if _, err := s.PostMessage(PostMessageRequest{RepoID: repoID, ID: 1, Kind: KindAnswer, Text: "a"}); err != nil {
		t.Fatal(err)
	}
	if len(h.answerRuns) != 0 {
		t.Fatalf("%+v", h.answerRuns)
	}
	b, _ := Load(repoID)
	it := b.Item(1)
	if len(it.Thread) != 2 || it.Thread[1].Kind != KindAnswer || len(it.Runs) != 1 || it.Status != StatusDone {
		t.Fatalf("%+v", it)
	}
}

func TestBeginAnswerRunLeavesBacklogOnError(t *testing.T) {
	s, _ := newTestService(t)
	mustAdd(t, s, ItemInput{Key: "a", Title: "A"})
	withRun(t, 1, StatusReview)
	b, _ := Load(repoID)
	if _, err := b.BeginAnswerRun(1, 0, "a", t0); err == nil || !strings.Contains(err.Error(), "no open question") {
		t.Fatal(err)
	}
	if it := b.Item(1); len(it.Runs) != 1 || it.Status != StatusReview || len(it.Thread) != 0 {
		t.Fatalf("%+v", it)
	}
}

func TestAnswerRunPrompt(t *testing.T) {
	b := New()
	b.Items = []Item{{ID: 4, Title: "Parent"}, {ID: 5, Title: "Child", Parent: 4, Body: "Do it."}}
	it := b.Item(5)
	q := Message{Kind: KindQuestion, Text: "Which db?", Handoff: "Tried:\n```\nsqlite\n```"}
	got := b.AnswerRunPrompt(it, "", PrevRun{Branch: "af/b-5", SHA: "abc123", Checked: true, PR: "https://x/pr/1"}, q, "Postgres")
	if !strings.HasPrefix(got, b.ItemPrompt(it, "")) {
		t.Fatalf("no item prompt first:\n%s", got)
	}
	for _, want := range []string{"- Previous branch: af/b-5\n", "- Last commit of that branch on origin: abc123\n",
		"- Pull request: https://x/pr/1\n", "````markdown\nTried:\n```\nsqlite\n```\n````\n",
		"```markdown\nWhich db?\n```\n", "The user's answer:\n\n```markdown\nPostgres\n```\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(b.AnswerRunPrompt(it, "", PrevRun{Branch: "af/b-5", Checked: true}, q, "a"), "origin does not have that branch") {
		t.Error("missing branch not said")
	}
	bare := b.AnswerRunPrompt(it, "", PrevRun{Branch: "af/b-5"}, Message{Text: "q"}, "a")
	if strings.Contains(bare, "origin") || strings.Contains(bare, "Notes from the previous agent") {
		t.Errorf("unchecked origin or empty handoff shown:\n%s", bare)
	}
}

func TestWithHandoff(t *testing.T) {
	if got := WithHandoff("p", "  "); got != "p" {
		t.Fatal(got)
	}
	got := WithHandoff("p", "notes")
	if !strings.HasPrefix(got, "p\n\n") || !strings.HasSuffix(got, "```markdown\nnotes\n```\n") {
		t.Fatal(got)
	}
}

func TestPendingAnswersCarryHandoff(t *testing.T) {
	s, h := newTestService(t)
	mustAdd(t, s, ItemInput{Key: "a", Title: "A"})
	withRun(t, 1, StatusRunning)
	if _, err := s.PostMessage(PostMessageRequest{RepoID: repoID, ID: 1, Kind: KindQuestion, Text: "q", SessionID: "s1",
		Handoff: "notes"}); err != nil {
		t.Fatal(err)
	}
	h.target = TargetRestore
	if _, err := s.PostMessage(PostMessageRequest{RepoID: repoID, ID: 1, Kind: KindAnswer, Text: "a"}); err != nil {
		t.Fatal(err)
	}
	b, _ := Load(repoID)
	if p := b.PendingAnswers(); len(p) != 1 || p[0].Handoff != "notes" || h.delivered[0].Handoff != "notes" {
		t.Fatalf("pending %+v delivered %+v", p, h.delivered)
	}
}

func TestServiceAnswerNamedQuestion(t *testing.T) {
	s, h := newTestService(t)
	mustAdd(t, s, ItemInput{Key: "a", Title: "A"})
	withRun(t, 1, StatusRunning)
	first := ask(t, s, "one")
	second := legacyQuestion(t, "two")
	h.target = TargetLive
	if _, err := s.PostMessage(PostMessageRequest{RepoID: repoID, ID: 1, Kind: KindAnswer, Text: "x", QuestionID: 99}); err == nil {
		t.Error("unknown question accepted")
	}
	out, err := s.PostMessage(PostMessageRequest{RepoID: repoID, ID: 1, Kind: KindAnswer, Text: "x", QuestionID: second.ID})
	if err != nil || out.Message.Answers != second.ID {
		t.Fatal(out, err)
	}
	if h.attention["s1"] != "one" {
		t.Fatalf("attention %q, want the question still open", h.attention["s1"])
	}
	if _, err := s.PostMessage(PostMessageRequest{RepoID: repoID, ID: 1, Kind: KindAnswer, Text: "x", QuestionID: second.ID}); err == nil {
		t.Error("an answered question accepted a second answer")
	}
	out, err = s.PostMessage(PostMessageRequest{RepoID: repoID, ID: 1, Kind: KindAnswer, Text: "y"})
	if err != nil || out.Message.Answers != first.ID || h.attention["s1"] != "" {
		t.Fatal(out, err, h.attention)
	}
}

func TestServiceMessageRefusals(t *testing.T) {
	s, _ := newTestService(t)
	mustAdd(t, s, ItemInput{Key: "a", Title: "A"})
	withRun(t, 1, StatusRunning)
	for _, req := range []PostMessageRequest{
		{RepoID: repoID, ID: 1, Kind: "comment", Text: "x"},
		{RepoID: repoID, ID: 1, Kind: KindQuestion, Text: "x"},
		{RepoID: repoID, ID: 1, Kind: KindQuestion, Text: " ", SessionID: "s1"},
		{RepoID: repoID, ID: 1, Kind: KindQuestion, Text: "x", SessionID: "s1", QuestionID: 1},
		{RepoID: repoID, ID: 1, Kind: KindAnswer, Text: "x", SessionID: "s1"},
		{RepoID: repoID, ID: 9, Kind: KindAnswer, Text: "x"},
	} {
		if _, err := s.PostMessage(req); err == nil {
			t.Errorf("%+v accepted", req)
		}
	}
}

func TestServiceDoneClosesQuestion(t *testing.T) {
	for _, to := range []Status{StatusDone, StatusDropped} {
		t.Run(string(to), func(t *testing.T) {
			s, h := newTestService(t)
			mustAdd(t, s, ItemInput{Key: "a", Title: "A"})
			withRun(t, 1, StatusRunning)
			ask(t, s, "q")
			if _, err := s.SetItemStatus(SetItemStatusRequest{RepoID: repoID, ID: 1, Status: to}); err != nil {
				t.Fatal(err)
			}
			if a, ok := h.attention["s1"]; !ok || a != "" {
				t.Fatalf("attention %q %v", a, ok)
			}
			b, _ := Load(repoID)
			if c := b.Item(1).Thread[0].Closed; c != string(to) {
				t.Fatal(c)
			}
		})
	}
}

func TestAnsweredInSession(t *testing.T) {
	now := time.Now()
	b := New()
	it, _ := b.Add(NewItem{Title: "A"}, now)
	it.Status = StatusRunning
	it.Runs = []RunRef{{SessionID: "old", State: RunStarted}, {SessionID: "s1", State: RunStarted}}
	if got := b.AnsweredInSession("s1", now); got != 0 {
		t.Fatalf("closed %d with nothing open", got)
	}
	if _, err := b.Ask(1, "old", Question{Text: "q"}, now); err == nil {
		t.Fatal("an old run asked")
	}
	if _, err := b.Ask(1, "s1", Question{Text: "q"}, now); err != nil {
		t.Fatal(err)
	}
	b.Item(1).appendMessage(Message{Kind: KindQuestion, Author: "agent:s1", Text: "r"}, now)
	if got := b.AnsweredInSession("old", now); got != 0 {
		t.Fatal("an old run closed the question")
	}
	if got := b.AnsweredInSession("s1", now); got != 1 {
		t.Fatal(got)
	}
	th := b.Item(1).Thread
	if th[0].Closed != ClosedAnsweredInSession || th[1].Closed != "" || b.OpenQuestionText(1) != "r" {
		t.Fatalf("only the oldest closes: %+v", th)
	}
}

func TestItemPromptSaysAsk(t *testing.T) {
	b := New()
	it, _ := b.Add(NewItem{Title: "A"}, time.Now())
	if p := b.ItemPrompt(it, ""); !strings.Contains(p, `af backlog ask --self`) {
		t.Fatal(p)
	}
}

func TestServiceAnswerWhileStarting(t *testing.T) {
	s, h := newTestService(t)
	mustAdd(t, s, ItemInput{Key: "a", Title: "A"})
	withRun(t, 1, StatusRunning)
	ask(t, s, "q")
	if _, err := Update(repoID, func(b *Backlog) error {
		it := b.Item(1)
		it.Runs = append(it.Runs, RunRef{State: RunStarting})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h.target = TargetLive
	_, err := s.PostMessage(PostMessageRequest{RepoID: repoID, ID: 1, Kind: KindAnswer, Text: "a"})
	if err == nil || !strings.Contains(err.Error(), "starting a new session") {
		t.Fatal(err)
	}
	b, _ := Load(repoID)
	if len(b.Item(1).Thread) != 1 {
		t.Fatal("a refused answer was written")
	}
}

func TestAnswerDelivered(t *testing.T) {
	b := New()
	it, _ := b.Add(NewItem{Title: "A"}, time.Now())
	it.Status = StatusReview
	it.Runs = []RunRef{{SessionID: "s1", State: RunStarted}}
	if _, err := b.Ask(1, "s1", Question{Text: "q"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	out, err := b.Answer(1, 0, "a", true, time.Now())
	if err != nil || b.Item(1).Status != StatusRunning || out.Answer.PrevStatus != StatusReview {
		t.Fatal(out, err)
	}
	if err := b.AnswerDelivered(1, out.Answer.ID, time.Now()); err != nil || len(b.PendingAnswers()) != 0 {
		t.Fatal(err, b.PendingAnswers())
	}
}
