package backlog

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// boundGroup is group 1 with children 2 and 3, bound to session "o".
func boundGroup(t *testing.T) *Backlog {
	t.Helper()
	b := mk(t, Item{ID: 1, Title: "Effort"}, Item{ID: 2, Parent: 1, Title: "Two"}, Item{ID: 3, Parent: 2, Title: "Three"}, Item{ID: 4, Title: "Alone"})
	if err := b.BindOrchestrator(1, orch("o"), t0); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestQueueOrchEventGoesToTheTopGroup(t *testing.T) {
	b := boundGroup(t)
	if g := b.QueueOrchEvent(OrchEvent{Kind: EventQuestion, Item: 3, Message: 1}, t0); g != 1 {
		t.Fatalf("group %d", g)
	}
	if g := b.QueueOrchEvent(OrchEvent{Kind: EventQuestion, Item: 3, Message: 1}, t0.Add(time.Second)); g != 1 {
		t.Fatalf("repeat group %d", g)
	}
	b.QueueOrchEvent(OrchEvent{Kind: EventFinished, Item: 1}, t0.Add(time.Minute))
	want := []OrchEvent{{Kind: EventQuestion, Item: 3, Message: 1, At: t0}, {Kind: EventFinished, Item: 1, At: t0.Add(time.Minute)}}
	if got := b.Item(1).Orchestrator.Events; !reflect.DeepEqual(got, want) {
		t.Fatalf("events %+v", got)
	}
	if g := b.QueueOrchEvent(OrchEvent{Kind: EventStalled, Item: 4}, t0); g != 0 {
		t.Fatalf("an unbound item queued on %d", g)
	}
	if g := b.QueueOrchEvent(OrchEvent{Kind: EventStalled, Item: 9}, t0); g != 0 {
		t.Fatalf("a missing item queued on %d", g)
	}
}

func TestOrchDeliveriesOnlyBoundGroupsWithEvents(t *testing.T) {
	b := boundGroup(t)
	if got := b.OrchDeliveries(t0); len(got) != 0 {
		t.Fatalf("no events, deliveries %+v", got)
	}
	b.QueueOrchEvent(OrchEvent{Kind: EventReview, Item: 2}, t0)
	got := b.OrchDeliveries(t0)
	if len(got) != 1 || got[0].Group != 1 || got[0].SessionID != "o" || got[0].Program != "claude" || len(got[0].Events) != 1 {
		t.Fatalf("%+v", got)
	}
	got[0].Events[0].Kind = "changed"
	if b.Item(1).Orchestrator.Events[0].Kind != EventReview {
		t.Fatal("a delivery shares the outbox")
	}
}

func TestOrchDeliveredDropsOnlyWhatWasSent(t *testing.T) {
	b := boundGroup(t)
	b.QueueOrchEvent(OrchEvent{Kind: EventReview, Item: 2}, t0)
	sent := b.OrchDeliveries(t0)[0].Events
	b.QueueOrchEvent(OrchEvent{Kind: EventStalled, Item: 3}, t0.Add(time.Second))
	b.OrchDeliveryFailed(1, "o", "x", t0)

	if b.OrchDelivered(1, "other", sent, t0) {
		t.Fatal("a session no longer bound dropped events")
	}
	if !b.OrchDelivered(1, "o", sent, t0.Add(2*time.Second)) {
		t.Fatal("not delivered")
	}
	o := b.Item(1).Orchestrator
	if len(o.Events) != 1 || o.Events[0].Kind != EventStalled || o.Failures != nil {
		t.Fatalf("%+v", o)
	}
}

func TestOrchDeliveryFailedBacksOffThenEscalates(t *testing.T) {
	b := boundGroup(t)
	b.QueueOrchEvent(OrchEvent{Kind: EventReview, Item: 2}, t0)

	if b.OrchDeliveryFailed(1, "o", "boom", t0) {
		t.Fatal("first failure escalated")
	}
	if got := b.OrchDeliveries(t0.Add(OrchRetryStep - time.Second)); len(got) != 0 {
		t.Fatal("retried inside the first wait")
	}
	at := t0.Add(OrchRetryStep)
	if got := b.OrchDeliveries(at); len(got) != 1 {
		t.Fatal("no retry after the first wait")
	}
	if b.OrchDeliveryFailed(1, "o", "boom", at) {
		t.Fatal("second failure escalated")
	}
	if got := b.OrchDeliveries(at.Add(2*OrchRetryStep - time.Second)); len(got) != 0 {
		t.Fatal("retried inside the second wait")
	}
	at = at.Add(2 * OrchRetryStep)
	if !b.OrchDeliveryFailed(1, "o", "restore failed", at) {
		t.Fatal("third failure in the window did not escalate")
	}
	g := b.Item(1)
	q := g.OpenQuestion()
	if q == nil || q.Author != AuthorAF || !strings.Contains(q.Text, `"t-o"`) || !strings.Contains(q.Text, "restore failed") {
		t.Fatalf("question %+v", q)
	}
	if !b.OrchEscalated(1) || len(b.OrchDeliveries(at.Add(time.Hour))) != 0 {
		t.Fatal("delivery goes on while escalated")
	}
	if len(g.Orchestrator.Events) != 1 {
		t.Fatal("escalation dropped the events")
	}
	if inbox := b.Inbox(); len(inbox) != 1 || inbox[0].ID != 1 {
		t.Fatalf("inbox %+v", inbox)
	}
}

func TestOrchDeliveryFailedForgetsOldFailures(t *testing.T) {
	b := boundGroup(t)
	b.OrchDeliveryFailed(1, "o", "x", t0)
	b.OrchDeliveryFailed(1, "o", "x", t0.Add(time.Minute))
	if b.OrchDeliveryFailed(1, "o", "x", t0.Add(OrchFailureWindow)) {
		t.Fatal("a failure outside the window counted")
	}
	if got := len(b.Item(1).Orchestrator.Failures); got != 2 {
		t.Fatalf("failures %d", got)
	}
	if b.OrchDeliveryFailed(1, "other", "x", t0.Add(OrchFailureWindow)) {
		t.Fatal("a session no longer bound escalated")
	}
}

func TestOrchReplacedRebindsAndKeepsTheRest(t *testing.T) {
	b := boundGroup(t)
	if _, err := b.SetOrchestratorHandoff("o", "notes", t0); err != nil {
		t.Fatal(err)
	}
	b.QueueOrchEvent(OrchEvent{Kind: EventReview, Item: 2}, t0)
	sent := append([]OrchEvent(nil), b.Item(1).Orchestrator.Events...)
	b.QueueOrchEvent(OrchEvent{Kind: EventStalled, Item: 3}, t0)
	b.OrchDeliveryFailed(1, "o", "x", t0)

	if err := b.OrchReplaced(1, "stale", orch("n"), sent, t0); err == nil {
		t.Fatal("replaced a session that is not bound")
	}
	if err := b.OrchReplaced(1, "o", orch("n"), sent, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	o := b.Item(1).Orchestrator
	if o.SessionID != "n" || o.Handoff != "notes" || len(o.Events) != 1 || o.Events[0].Kind != EventStalled ||
		o.Failures != nil || !o.BoundAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("%+v", o)
	}
}

func TestOrchOutboxSurvivesASave(t *testing.T) {
	b := boundGroup(t)
	b.QueueOrchEvent(OrchEvent{Kind: EventQuestion, Item: 3, Message: 2}, t0)
	b.OrchDeliveryFailed(1, "o", "x", t0)
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var back Backlog
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	o := back.Item(1).Orchestrator
	if len(o.Events) != 1 || len(o.Failures) != 1 || !o.Failures[0].Equal(t0) {
		t.Fatalf("%+v", o)
	}
	if !back.OrchDelivered(1, "o", back.OrchDeliveries(t0.Add(OrchRetryStep))[0].Events, t0) || len(back.Item(1).Orchestrator.Events) != 0 {
		t.Fatal("events read back did not match the outbox")
	}
}

func TestOrchPrompts(t *testing.T) {
	b := boundGroup(t)
	b.Item(2).PR = "https://example.test/pr/1"
	if _, err := b.SetOrchestratorHandoff("o", "Merge B-2 first.", t0); err != nil {
		t.Fatal(err)
	}
	events := []OrchEvent{{Kind: EventQuestion, Item: 3}, {Kind: EventReview, Item: 2}, {Kind: EventStalled, Item: 3},
		{Kind: EventFinished, Item: 1}, {Kind: EventEscalationAnswered, Item: 3}}
	p := b.OrchEventsPrompt(1, events)
	for _, want := range []string{"Events in effort B-1:", "- B-3 Three: the agent asked a question.",
		"- B-2 Two: ready for review, https://example.test/pr/1.", "- B-3 Three: stalled.",
		"- B-1 Effort: every item is done or dropped.", "the user answered the question you escalated", `af backlog show B-1`} {
		if !strings.Contains(p, want) {
			t.Errorf("events prompt lacks %q:\n%s", want, p)
		}
	}

	p = b.OrchStartPrompt(1, "/p/effort", true, events[:1])
	for _, want := range []string{"You orchestrate backlog effort B-1: Effort", "proposal in /p/effort",
		"af started this session to replace it", "Merge B-2 first.", "--handoff-file", "the agent asked a question"} {
		if !strings.Contains(p, want) {
			t.Errorf("start prompt lacks %q:\n%s", want, p)
		}
	}
	if p = b.OrchStartPrompt(1, "", false, nil); !strings.Contains(p, "restarted without its earlier conversation") ||
		strings.Contains(p, "Events in") || strings.Contains(p, "proposal in") {
		t.Errorf("restart prompt:\n%s", p)
	}
}
