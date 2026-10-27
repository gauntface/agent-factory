package backlog

import (
	"testing"
	"time"
)

func TestOrchTurnEndedArchivesWhenNothingIsPending(t *testing.T) {
	b := boundGroup(t)
	end := b.OrchTurnEnded("o", t0)
	if end != (OrchTurnEnd{Group: 1, Archive: true}) {
		t.Fatalf("%+v", end)
	}
	if !b.ArchiveOwed("o", ArchiveOrchIdle) {
		t.Fatal("idle archive not owed")
	}
	if b.ArchiveOwed("o", ArchiveAsk) || b.ArchiveOwed("x", ArchiveOrchIdle) {
		t.Fatal("wrong archive owed")
	}
	if end := b.OrchTurnEnded("x", t0); end != (OrchTurnEnd{}) {
		t.Fatalf("an unbound session: %+v", end)
	}
}

func TestOrchTurnEndedKeepsLiveWithPendingEvents(t *testing.T) {
	b := boundGroup(t)
	b.QueueOrchEvent(OrchEvent{Kind: EventReview, Item: 2}, t0)
	if end := b.OrchTurnEnded("o", t0); end.Archive || end.Changed || end.Group != 1 {
		t.Fatalf("%+v", end)
	}
	if b.ArchiveOwed("o", ArchiveOrchIdle) {
		t.Fatal("archive owed with an event to send")
	}
}

func TestOrchStayKeepsLiveForOneTurn(t *testing.T) {
	b := boundGroup(t)
	if _, err := b.SetOrchestratorStay("x", t0); err == nil {
		t.Fatal("an unbound session stayed")
	}
	at := t0.Add(time.Minute)
	if id, err := b.SetOrchestratorStay("o", at); err != nil || id != 1 {
		t.Fatal(id, err)
	}
	if !b.Item(1).Orchestrator.Stay || !b.Item(1).UpdatedAt.Equal(at) {
		t.Fatal("stay not stored")
	}
	if b.ArchiveOwed("o", ArchiveOrchIdle) {
		t.Fatal("archive owed while the session asked to stay")
	}
	if end := b.OrchTurnEnded("o", t0); end != (OrchTurnEnd{Group: 1, Changed: true}) {
		t.Fatalf("%+v", end)
	}
	if b.Item(1).Orchestrator.Stay {
		t.Fatal("stay not used up")
	}
	if end := b.OrchTurnEnded("o", t0); !end.Archive {
		t.Fatalf("next turn: %+v", end)
	}
}

func TestKeptLiveAddsNoNoteToAnOrchestrator(t *testing.T) {
	b := boundGroup(t)
	n := len(b.Item(1).Thread)
	if id, err := b.KeptLive("o", ArchiveOrchIdle, "the session changed", t0); err != nil || id != 0 {
		t.Fatal(id, err)
	}
	if len(b.Item(1).Thread) != n {
		t.Fatal("note added")
	}
}

func TestSetOrchestratorMeters(t *testing.T) {
	b := boundGroup(t)
	m := map[string]Meter{"a.jsonl": {Offset: 10, Cost: 1.5}, "b.jsonl": {Offset: 4, Cost: 0.25}}
	if b.SetOrchestratorMeters(map[string]map[string]Meter{"x": m}) {
		t.Fatal("an unbound session changed the backlog")
	}
	if !b.SetOrchestratorMeters(map[string]map[string]Meter{"o": m}) {
		t.Fatal("no change")
	}
	if b.SetOrchestratorMeters(map[string]map[string]Meter{"o": m}) {
		t.Fatal("same meters changed the backlog")
	}
	m["a.jsonl"] = Meter{Offset: 99}
	o := b.Item(1).Orchestrator
	if o.Meter["a.jsonl"].Offset != 10 || o.Cost() != 1.75 {
		t.Fatalf("%+v", o.Meter)
	}
	if err := b.BindOrchestrator(1, orch("o2"), t0); err != nil {
		t.Fatal(err)
	}
	if b.Item(1).Orchestrator.Cost() != 1.75 {
		t.Fatal("a replacement lost the meter")
	}
}
