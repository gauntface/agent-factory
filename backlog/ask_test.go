package backlog

import (
	"strings"
	"testing"
	"time"
)

func runningInS1(t *testing.T) *Backlog {
	t.Helper()
	b := New()
	it, err := b.Add(NewItem{Title: "A"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	it.Status = StatusRunning
	it.Runs = []RunRef{{SessionID: "s1", State: RunStarted}}
	return b
}

func TestAskOneOpenQuestion(t *testing.T) {
	b := runningInS1(t)
	m, err := b.Ask(1, "s1", Question{Text: "q", Then: ThenArchive, Handoff: " notes "}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if m.Then != ThenArchive || m.Handoff != "notes" {
		t.Fatalf("%+v", m)
	}
	_, err = b.Ask(1, "s1", Question{Text: "r"}, time.Now())
	if err == nil || err.Error() != "B-1 already has an open question; wait for the answer" {
		t.Fatal(err)
	}
	if _, err := b.Answer(1, 0, "a", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Ask(1, "s1", Question{Text: "r"}, time.Now()); err != nil {
		t.Fatalf("ask after the answer: %v", err)
	}
}

func TestAskChecksThenAndHandoff(t *testing.T) {
	for _, q := range []Question{
		{Text: "q", Then: "kill"},
		{Text: "q", Handoff: strings.Repeat("x", MaxHandoffBytes+1)},
	} {
		if _, err := runningInS1(t).Ask(1, "s1", q, time.Now()); err == nil {
			t.Errorf("then %q handoff %d bytes accepted", q.Then, len(q.Handoff))
		}
	}
	if _, err := runningInS1(t).Ask(1, "s1", Question{Text: "q", Handoff: strings.Repeat("x", MaxHandoffBytes)}, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestRunEndedOnAsk(t *testing.T) {
	for _, tc := range []struct {
		then    string
		archive bool
	}{{"", false}, {ThenWait, false}, {ThenArchive, true}} {
		b := runningInS1(t)
		if _, err := b.Ask(1, "s1", Question{Text: "q", Then: tc.then}, time.Now()); err != nil {
			t.Fatal(err)
		}
		end := b.RunEnded("s1", time.Now())
		if end.Archive != tc.archive || end.Changed || b.Item(1).Status != StatusRunning {
			t.Errorf("then %q: %+v, status %s", tc.then, end, b.Item(1).Status)
		}
		if end.Archive && end.Reason != ArchiveAsk {
			t.Errorf("reason %q", end.Reason)
		}
		if got := b.ArchiveOwed("s1", ArchiveAsk); got != tc.archive {
			t.Errorf("then %q: owed %v", tc.then, got)
		}
	}
}

func TestArchiveOwed(t *testing.T) {
	b := runningInS1(t)
	if _, err := b.Ask(1, "s1", Question{Text: "q", Then: ThenArchive}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !b.ArchiveOwed("s1", ArchiveAsk) || b.ArchiveOwed("s1", ArchiveDone) || b.ArchiveOwed("s2", ArchiveAsk) {
		t.Fatal("owed")
	}
	b.Item(1).Runs = append(b.Item(1).Runs, RunRef{SessionID: "s2", State: RunStarted})
	if b.ArchiveOwed("s1", ArchiveAsk) {
		t.Fatal("an older run is owed an archive")
	}
	b.Item(1).Runs = b.Item(1).Runs[:1]
	if _, err := b.Answer(1, 0, "a", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if b.ArchiveOwed("s1", ArchiveAsk) {
		t.Fatal("an answer did not cancel the archive")
	}
	b.Item(1).Status = StatusDone
	if b.ArchiveOwed("s1", ArchiveDone) {
		t.Fatal("done is owed with on_done keep")
	}
	b.Settings.OnDone = OnDoneArchive
	if !b.ArchiveOwed("s1", ArchiveDone) {
		t.Fatal("done is not owed with on_done archive")
	}
}

func TestKeptLive(t *testing.T) {
	b := runningInS1(t)
	if _, err := b.Ask(1, "s1", Question{Text: "q", Then: ThenArchive}, time.Now()); err != nil {
		t.Fatal(err)
	}
	id, err := b.KeptLive("s1", ArchiveAsk, "the session changed after its run ended", time.Now())
	if err != nil || id != 1 {
		t.Fatal(id, err)
	}
	th := b.Item(1).Thread
	if th[0].Then != ThenWait {
		t.Fatalf("then %q", th[0].Then)
	}
	if last := th[len(th)-1]; last.Author != AuthorAF || last.Text != "Kept the session live: the session changed after its run ended" {
		t.Fatalf("%+v", last)
	}
	if b.RunEnded("s1", time.Now()).Archive {
		t.Fatal("a kept session is archived at its next run end")
	}
	if id, err := b.KeptLive("nope", ArchiveAsk, "x", time.Now()); id != 0 || err != nil {
		t.Fatal(id, err)
	}
}

func TestKeptLiveQuietAfterAnswer(t *testing.T) {
	b := runningInS1(t)
	if _, err := b.Ask(1, "s1", Question{Text: "q", Then: ThenArchive}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Answer(1, 0, "a", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	n := len(b.Item(1).Thread)
	if id, err := b.KeptLive("s1", ArchiveAsk, "a new run started", time.Now()); id != 0 || err != nil || len(b.Item(1).Thread) != n {
		t.Fatalf("id %d err %v thread %+v", id, err, b.Item(1).Thread)
	}
}

func TestInbox(t *testing.T) {
	now := time.Now()
	b := New()
	for _, title := range []string{"G", "A", "B"} {
		if _, err := b.Add(NewItem{Title: title}, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Add(NewItem{Title: "C", Parent: 1}, now); err != nil {
		t.Fatal(err)
	}
	b.Item(3).appendMessage(Message{Kind: KindQuestion, Text: "b1"}, now.Add(1*time.Minute))
	b.Item(2).appendMessage(Message{Kind: KindQuestion, Text: "a1"}, now.Add(2*time.Minute))
	b.Item(3).appendMessage(Message{Kind: KindQuestion, Text: "b2"}, now.Add(3*time.Minute))
	b.Item(2).appendMessage(Message{Kind: KindQuestion, Text: "closed", Closed: ClosedAnswered}, now)
	b.Item(4).appendMessage(Message{Kind: KindQuestion, Text: "hidden"}, now)
	b.Item(1).Archived = true
	var got []string
	for _, e := range b.Inbox() {
		got = append(got, FormatID(e.ID)+" "+e.Question.Text)
	}
	if strings.Join(got, ", ") != "B-3 b1, B-2 a1, B-3 b2" {
		t.Fatal(got)
	}
	if New().Inbox() == nil {
		t.Fatal("an empty inbox is nil")
	}
}

func TestServiceThenOnlyOnQuestions(t *testing.T) {
	s, _ := newTestService(t)
	mustAdd(t, s, ItemInput{Key: "a", Title: "A"})
	withRun(t, 1, StatusRunning)
	if _, err := s.PostMessage(PostMessageRequest{RepoID: repoID, ID: 1, Kind: KindNote, Text: "x", Then: ThenArchive}); err == nil {
		t.Fatal("then on a note accepted")
	}
	out, err := s.PostMessage(PostMessageRequest{RepoID: repoID, ID: 1, Kind: KindQuestion, Text: "q", SessionID: "s1",
		Then: ThenArchive, Handoff: "h"})
	if err != nil || out.Message.Then != ThenArchive || out.Message.Handoff != "h" {
		t.Fatal(out, err)
	}
}
