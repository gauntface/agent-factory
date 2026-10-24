package backlog

import (
	"reflect"
	"strings"
	"testing"
)

// group is B-1 > B-2 > B-3 (queued), with B-4 a queued top-level leaf.
func group(t *testing.T) *Backlog {
	return mk(t,
		Item{ID: 1},
		Item{ID: 2, Parent: 1},
		Item{ID: 3, Parent: 2, Status: StatusQueued},
		Item{ID: 4, Status: StatusQueued},
	)
}

func TestPauseSkipsSubtree(t *testing.T) {
	b := group(t)
	if _, err := b.Edit(1, Edit{Paused: ptr(true)}, t0); err != nil {
		t.Fatal(err)
	}
	states := b.DeriveAll(nil)
	if got, want := states[3], (Derived{State: StateQueued, Reason: "Queued · paused (B-1)"}); got != want {
		t.Fatalf("B-3 = %+v, want %+v", got, want)
	}
	if got := b.Runnable(states, 5, nil); !reflect.DeepEqual(got, []int{4}) {
		t.Fatalf("Runnable = %v, want [4]", got)
	}

	if _, err := b.Edit(2, Edit{Paused: ptr(true)}, t0); err != nil {
		t.Fatal(err)
	}
	if got := b.Derive(3, nil).Reason; got != "Queued · paused (B-2)" {
		t.Fatalf("nearest paused ancestor: reason %q", got)
	}

	for _, id := range []int{1, 2} {
		if _, err := b.Edit(id, Edit{Paused: ptr(false)}, t0); err != nil {
			t.Fatal(err)
		}
	}
	states = b.DeriveAll(nil)
	if got := b.Runnable(states, 5, nil); !reflect.DeepEqual(got, []int{3, 4}) {
		t.Fatalf("after unpause Runnable = %v", got)
	}
}

func TestPauseWaitingKeepsWaitReason(t *testing.T) {
	b := mk(t, Item{ID: 1, Paused: true}, Item{ID: 2, Parent: 1, Status: StatusQueued, WaitsOn: []int{3}}, Item{ID: 3})
	if got := b.Derive(2, nil); got.State != StateWaiting {
		t.Fatalf("B-2 = %+v, want waiting", got)
	}
}

func TestPausePlanRunner(t *testing.T) {
	b := group(t)
	b.Settings.Runner = true
	b.Item(1).Paused = true
	b.Item(4).Status = StatusDraft
	plan := b.PlanRunner(RunnerInput{States: b.DeriveAll(nil), Limit: -1})
	if len(plan.Start) != 0 || plan.View.State != RunnerIdle {
		t.Fatalf("plan = %+v, want idle and no starts", plan)
	}
}

func TestPauseErrors(t *testing.T) {
	b := group(t)
	_, err := b.Edit(3, Edit{Paused: ptr(true)}, t0)
	if err == nil || err.Error() != "B-3 is not a parent; only parents can be paused" {
		t.Fatalf("pause leaf: %v", err)
	}
	if b.Item(3).Paused {
		t.Fatal("failed edit changed the item")
	}
}

func TestArchive(t *testing.T) {
	b := group(t)
	if _, err := b.Edit(1, Edit{Archived: ptr(true)}, t0); err != nil {
		t.Fatal(err)
	}
	if g := b.Item(1); !g.Archived || !g.Paused {
		t.Fatalf("archive: %+v, want archived and paused", g)
	}
	if b.ArchivedBy(3) != 1 || b.ArchivedBy(4) != 0 {
		t.Fatalf("ArchivedBy: B-3 %d, B-4 %d", b.ArchivedBy(3), b.ArchivedBy(4))
	}
	_, err := b.Edit(1, Edit{Paused: ptr(false)}, t0)
	if err == nil || !strings.Contains(err.Error(), "B-1 is archived; unarchive it first") {
		t.Fatalf("unpause archived: %v", err)
	}
	if err := b.Move(1, Move{Into: 4}, t0); err == nil || !strings.Contains(err.Error(), "archived") {
		t.Fatalf("move archived into parent: %v", err)
	}
	if _, err := b.Edit(1, Edit{Archived: ptr(false)}, t0); err != nil {
		t.Fatal(err)
	}
	if g := b.Item(1); g.Archived || !g.Paused {
		t.Fatalf("unarchive: %+v, want paused and not archived", g)
	}
}

func TestArchiveRefusals(t *testing.T) {
	cases := []struct {
		name string
		id   int
		edit func(b *Backlog)
		want string
	}{
		{"leaf", 4, nil, "B-4 is not a top-level parent; only groups can be archived"},
		{"nested parent", 2, nil, "B-2 is not a top-level parent; only groups can be archived"},
		{"running item", 1, func(b *Backlog) { b.Item(3).Status = StatusRunning },
			"B-1 has a running item (B-3); stop it before you archive B-1"},
		{"open question", 1, func(b *Backlog) {
			b.Item(3).Thread = []Message{{ID: 1, Kind: KindQuestion, Text: "?"}}
		}, "B-1 has an open question on B-3; answer it before you archive B-1"},
		{"answer on its way", 1, func(b *Backlog) {
			b.Item(3).Thread = []Message{{ID: 1, Kind: KindQuestion, Text: "?"},
				{ID: 2, Kind: KindAnswer, Answers: 1, Text: "!", Delivery: DeliveryPending}}
		}, "B-1 has an answer on its way to B-3; wait for it before you archive B-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := group(t)
			if tc.edit != nil {
				tc.edit(b)
			}
			_, err := b.Edit(tc.id, Edit{Archived: ptr(true)}, t0)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if b.Item(tc.id).Archived || b.Item(tc.id).Paused {
				t.Fatal("failed edit changed the item")
			}
		})
	}
}
