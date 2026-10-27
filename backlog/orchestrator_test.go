package backlog

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func orch(id string) Orchestrator {
	return Orchestrator{SessionID: id, Title: "t-" + id, Program: "claude"}
}

func TestBindOrchestratorOnlyOnGroups(t *testing.T) {
	b := mk(t, Item{ID: 1}, Item{ID: 2, Parent: 1}, Item{ID: 3, Parent: 2}, Item{ID: 4})
	for _, id := range []int{2, 3, 4} {
		if err := b.BindOrchestrator(id, orch("s"), t0); err == nil || !strings.Contains(err.Error(), "not a top-level parent") {
			t.Errorf("%s: %v", FormatID(id), err)
		}
	}
	if err := b.BindOrchestrator(9, orch("s"), t0); err == nil {
		t.Error("bound a missing item")
	}
	if err := b.BindOrchestrator(1, orch(" "), t0); err == nil {
		t.Error("bound an empty session id")
	}
	if err := b.BindOrchestrator(1, orch("s"), t0); err != nil {
		t.Fatal(err)
	}
	o := b.Item(1).Orchestrator
	if o == nil || o.SessionID != "s" || o.Title != "t-s" || !o.BoundAt.Equal(t0) || !b.Item(1).UpdatedAt.Equal(t0) {
		t.Fatalf("%+v", o)
	}
	if b.OrchestratedBy("s") != 1 || b.OrchestratedBy("x") != 0 {
		t.Fatal(b.OrchestratedBy("s"))
	}
}

func TestBindOrchestratorOneGroupPerSession(t *testing.T) {
	b := mk(t, Item{ID: 1}, Item{ID: 2, Parent: 1}, Item{ID: 3}, Item{ID: 4, Parent: 3})
	if err := b.BindOrchestrator(1, orch("s"), t0); err != nil {
		t.Fatal(err)
	}
	err := b.BindOrchestrator(3, orch("s"), t0)
	if err == nil || err.Error() != `session "s" already orchestrates B-1; clear it first` {
		t.Fatal(err)
	}
	if b.Item(3).Orchestrator != nil {
		t.Fatal("refused bind changed the item")
	}
	if err := b.BindOrchestrator(1, orch("s"), t0); err != nil {
		t.Fatalf("rebind same group: %v", err)
	}
	if _, err := b.ClearOrchestrator(1, t0); err != nil {
		t.Fatal(err)
	}
	if err := b.BindOrchestrator(3, orch("s"), t0); err != nil {
		t.Fatalf("bind after clear: %v", err)
	}
}

func TestBindOrchestratorReplacementKeepsOutbox(t *testing.T) {
	b := mk(t, Item{ID: 1}, Item{ID: 2, Parent: 1})
	if err := b.BindOrchestrator(1, orch("old"), t0); err != nil {
		t.Fatal(err)
	}
	g := b.Item(1)
	g.Orchestrator.Handoff = "notes"
	g.Orchestrator.Meter = map[string]Meter{"x": {Cost: 1}}
	g.Orchestrator.Events = []OrchEvent{{Kind: EventQuestion, Item: 2, Message: 1, At: t0}}
	later := t0.Add(1)
	if err := b.BindOrchestrator(1, orch("new"), later); err != nil {
		t.Fatal(err)
	}
	o := b.Item(1).Orchestrator
	if o.SessionID != "new" || o.Handoff != "notes" || o.Meter["x"].Cost != 1 || len(o.Events) != 1 || !o.BoundAt.Equal(later) {
		t.Fatalf("%+v", o)
	}
	if b.OrchestratedBy("old") != 0 {
		t.Fatal("old session still bound")
	}
}

func TestClearOrchestrator(t *testing.T) {
	b := mk(t, Item{ID: 1}, Item{ID: 2, Parent: 1})
	if prev, err := b.ClearOrchestrator(1, t0); err != nil || prev != nil {
		t.Fatal(prev, err)
	}
	if _, err := b.ClearOrchestrator(9, t0); err == nil {
		t.Fatal("cleared a missing item")
	}
	if err := b.BindOrchestrator(1, orch("s"), t0); err != nil {
		t.Fatal(err)
	}
	prev, err := b.ClearOrchestrator(1, t0)
	if err != nil || prev == nil || prev.SessionID != "s" || b.Item(1).Orchestrator != nil {
		t.Fatal(prev, err)
	}
}

func TestOrchestratedGroupStaysGroup(t *testing.T) {
	b := mk(t, Item{ID: 1}, Item{ID: 2, Parent: 1}, Item{ID: 3}, Item{ID: 4, Parent: 3})
	if err := b.BindOrchestrator(1, orch("s"), t0); err != nil {
		t.Fatal(err)
	}
	if err := b.Move(1, Move{Into: 3}, t0); err == nil || !strings.Contains(err.Error(), "has an orchestrator") {
		t.Fatalf("moved into a parent: %v", err)
	}
	if err := b.Move(2, Move{Into: 3}, t0); err == nil {
		t.Fatal("moved the last child out")
	}
	if _, err := b.Delete(2); err == nil || !strings.Contains(err.Error(), "last item in B-1") {
		t.Fatalf("deleted the last child: %v", err)
	}
	if b.Item(1).Orchestrator == nil || b.Item(2) == nil {
		t.Fatal("refused change applied")
	}
	if err := b.Move(1, Move{After: 3}, t0); err != nil {
		t.Fatalf("top-level move: %v", err)
	}
	if _, err := b.Add(NewItem{Title: "more", Parent: 1}, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Delete(2); err != nil {
		t.Fatalf("delete with a sibling left: %v", err)
	}
}

func TestNormalizeDropsBadOrchestrators(t *testing.T) {
	b := mk(t,
		Item{ID: 1, Orchestrator: &Orchestrator{SessionID: "a"}},
		Item{ID: 2, Parent: 1, Orchestrator: &Orchestrator{SessionID: "b"}},
		Item{ID: 3, Orchestrator: &Orchestrator{SessionID: "c"}},
		Item{ID: 4, Orchestrator: &Orchestrator{SessionID: "a"}},
		Item{ID: 5, Parent: 4},
		Item{ID: 6, Orchestrator: &Orchestrator{}},
		Item{ID: 7, Parent: 6},
	)
	if b.Item(1).Orchestrator == nil {
		t.Fatal("dropped a good binding")
	}
	for _, id := range []int{2, 3, 4, 6} {
		if b.Item(id).Orchestrator != nil {
			t.Errorf("%s kept its binding", FormatID(id))
		}
	}
}

func TestCloneCopiesOrchestrator(t *testing.T) {
	b := mk(t, Item{ID: 1}, Item{ID: 2, Parent: 1})
	if err := b.BindOrchestrator(1, orch("s"), t0); err != nil {
		t.Fatal(err)
	}
	o := b.Item(1).Orchestrator
	o.Meter = map[string]Meter{"x": {Cost: 1}}
	o.Events = []OrchEvent{{Kind: EventReview, Item: 2}}
	c := b.clone()
	co := c.Item(1).Orchestrator
	co.SessionID = "other"
	co.Meter["x"] = Meter{Cost: 2}
	co.Events[0].Kind = EventStalled
	if o.SessionID != "s" || o.Meter["x"].Cost != 1 || o.Events[0].Kind != EventReview {
		t.Fatalf("clone shares state: %+v", o)
	}
}

func TestMigratesV2Unchanged(t *testing.T) {
	home(t)
	path, _ := BacklogPath(repoID)
	writeRaw(t, path, `{"schema_version": 2, "next_id": 3, "items": [
		{"id": 1, "title": "group", "status": "draft", "paused": true},
		{"id": 2, "title": "child", "status": "queued", "parent": 1}]}`)
	b, err := Load(repoID)
	if err != nil {
		t.Fatal(err)
	}
	if b.SchemaVersion != 3 || !b.Item(1).Paused || b.Item(1).Orchestrator != nil || b.Item(2).Status != StatusQueued {
		t.Fatalf("%+v", b)
	}
	if _, err := Update(repoID, func(b *Backlog) error {
		return b.BindOrchestrator(1, orch("s"), t0)
	}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var obj struct {
		SchemaVersion int `json:"schema_version"`
		Items         []struct {
			Orchestrator *Orchestrator `json:"orchestrator"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	if obj.SchemaVersion != 3 || obj.Items[0].Orchestrator == nil || obj.Items[0].Orchestrator.SessionID != "s" || obj.Items[1].Orchestrator != nil {
		t.Fatalf("%s", raw)
	}
	if strings.Contains(string(raw), `"handoff"`) || strings.Contains(string(raw), `"events"`) || strings.Contains(string(raw), `"meter"`) {
		t.Fatalf("empty fields written: %s", raw)
	}
}
