package backlog

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type testHost struct {
	name    string
	changed [][]int
}

func (h *testHost) Facts(string) Facts          { return nil }
func (h *testHost) ProjectName(string) string   { return h.name }
func (h *testHost) Changed(_ string, ids []int) { h.changed = append(h.changed, ids) }
func (h *testHost) last() []int                 { return h.changed[len(h.changed)-1] }
func ptr[T any](v T) *T                         { return &v }
func (h *testHost) reset()                      { h.changed = nil }
func newTestService(t *testing.T) (*Service, *testHost) {
	t.Helper()
	home(t)
	h := &testHost{name: "agent-factory"}
	s := NewService(h)
	s.now = func() time.Time { return time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC) }
	return s, h
}

func mustAdd(t *testing.T, s *Service, items ...ItemInput) map[string]int {
	t.Helper()
	out, err := s.Add(AddRequest{RepoID: repoID, Items: items})
	if err != nil {
		t.Fatal(err)
	}
	return out.IDs
}

func TestServiceResolve(t *testing.T) {
	s, _ := newTestService(t)
	for _, req := range []GetRequest{{}, {RepoID: repoID, RepoPath: "/x"}} {
		if _, err := s.Get(req); err == nil || !strings.Contains(err.Error(), "exactly one of repo_id or repo_path") {
			t.Errorf("%+v: %v", req, err)
		}
	}
	if _, err := s.Get(GetRequest{RepoID: "../x"}); err == nil {
		t.Error("bad repo id accepted")
	}
}

func TestServiceRepoPath(t *testing.T) {
	s, h := newTestService(t)
	h.name = ""
	dir := filepath.Join(t.TempDir(), "myproj")
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if _, err := s.Add(AddRequest{RepoPath: dir, Items: []ItemInput{{Key: "a", Title: "A"}}}); err != nil {
		t.Fatal(err)
	}
	_, err := s.UpdateItem(UpdateItemRequest{RepoPath: dir, ID: 9, Title: ptr("x")})
	if err == nil || err.Error() != `B-9 not found in project myproj — run "af backlog list --all"` {
		t.Fatal(err)
	}
}

func TestServiceGetEmpty(t *testing.T) {
	s, _ := newTestService(t)
	v, err := s.Get(GetRequest{RepoID: repoID})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Backlog.Items) != 0 || len(v.States) != 0 {
		t.Fatalf("%+v", v)
	}
	want := RunnerView{State: RunnerOff, Reason: "Items run only when you start them.", Max: 1}
	if v.Runner != want {
		t.Fatalf("runner %+v", v.Runner)
	}
}

func TestServiceAdd(t *testing.T) {
	s, h := newTestService(t)
	ids := mustAdd(t, s, ItemInput{Key: "p", Title: "Parent", Sequential: true},
		ItemInput{Key: "a", Title: "A", Parent: Ref{Key: "p"}},
		ItemInput{Key: "b", Title: "B", Parent: Ref{Key: "p"}, WaitsOn: []Ref{{Key: "a"}}})
	if !reflect.DeepEqual(ids, map[string]int{"p": 1, "a": 2, "b": 3}) {
		t.Fatal(ids)
	}
	if !reflect.DeepEqual(h.last(), []int{1, 2, 3}) {
		t.Fatal(h.changed)
	}
	out, err := s.Add(AddRequest{RepoID: repoID, Items: []ItemInput{{Key: "item", Title: "First", Parent: Ref{ID: 1}}}, Before: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != 4 || out.Items[0].Status != StatusDraft {
		t.Fatalf("%+v", out)
	}
	v, _ := s.Get(GetRequest{RepoID: repoID})
	if got := v.Backlog.Children(1); !reflect.DeepEqual(got, []int{4, 2, 3}) {
		t.Fatal(got)
	}
	if v.States[3].State != StateDraft {
		t.Fatal(v.States[3])
	}

	if _, err := s.Add(AddRequest{RepoID: repoID, Items: []ItemInput{{Title: "a"}, {Title: "b"}}, After: 1}); err == nil {
		t.Error("before/after with a batch accepted")
	}
	if _, err := s.Add(AddRequest{RepoID: repoID, Items: []ItemInput{{Title: "a", WaitsOn: []Ref{{Key: "x"}}}}, After: 1}); err == nil {
		t.Error("key ref with after accepted")
	}
	if _, err := s.Add(AddRequest{RepoID: repoID, Items: []ItemInput{{Title: "x", WaitsOn: []Ref{{ID: 99}}}}}); err == nil ||
		!strings.Contains(err.Error(), "in project agent-factory") {
		t.Errorf("not found without project: %v", err)
	}
}

func TestServiceUpdateMoveDelete(t *testing.T) {
	s, h := newTestService(t)
	mustAdd(t, s, ItemInput{Key: "a", Title: "A"}, ItemInput{Key: "b", Title: "B"}, ItemInput{Key: "c", Title: "C"})
	out, err := s.UpdateItem(UpdateItemRequest{RepoID: repoID, ID: 2, Title: ptr("B2"), WaitsOn: ptr([]int{1})})
	if err != nil || out.Item.Title != "B2" || !reflect.DeepEqual(out.Item.WaitsOn, []int{1}) {
		t.Fatal(out, err)
	}
	if _, err := s.UpdateItem(UpdateItemRequest{RepoID: repoID, ID: 1, WaitsOn: ptr([]int{2})}); err == nil ||
		!strings.Contains(err.Error(), "cannot wait on") {
		t.Fatal(err)
	}
	if err := s.Move(MoveRequest{RepoID: repoID, ID: 3, Top: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Move(MoveRequest{RepoID: repoID, ID: 2, Into: 3}); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Get(GetRequest{RepoID: repoID})
	var order []int
	for _, it := range v.Backlog.Items {
		order = append(order, it.ID)
	}
	if !reflect.DeepEqual(order, []int{3, 2, 1}) || v.Backlog.Item(2).Parent != 3 {
		t.Fatal(order)
	}
	if err := s.DeleteItem(DeleteItemRequest{RepoID: repoID, ID: 3}); err == nil || !strings.Contains(err.Error(), "has children") {
		t.Fatal(err)
	}
	h.reset()
	if err := s.DeleteItem(DeleteItemRequest{RepoID: repoID, ID: 1}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.changed, [][]int{{1}}) {
		t.Fatal(h.changed)
	}
	v, _ = s.Get(GetRequest{RepoID: repoID})
	if v.Backlog.Item(1) != nil || len(v.Backlog.Item(2).WaitsOn) != 0 {
		t.Fatalf("%+v", v.Backlog.Items)
	}
}

func TestServiceDeleteRefusals(t *testing.T) {
	s, _ := newTestService(t)
	mustAdd(t, s, ItemInput{Key: "a", Title: "A"}, ItemInput{Key: "b", Title: "B"})
	if _, err := s.SetItemStatus(SetItemStatusRequest{RepoID: repoID, ID: 1, Status: StatusQueued}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteItem(DeleteItemRequest{RepoID: repoID, ID: 1}); err == nil || err.Error() != "B-1 is queued; only a draft can be deleted — drop it instead" {
		t.Fatal(err)
	}
	if _, err := s.PostMessage(PostMessageRequest{RepoID: repoID, ID: 2, Kind: KindNote, Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteItem(DeleteItemRequest{RepoID: repoID, ID: 2}); err == nil || err.Error() != "B-2 is a draft with runs or a thread; drop it instead" {
		t.Fatal(err)
	}
	if err := s.DeleteItem(DeleteItemRequest{RepoID: repoID, ID: 7}); err == nil || err.Error() != `B-7 not found in project agent-factory — run "af backlog list --all"` {
		t.Fatal(err)
	}
}

func TestServiceStatus(t *testing.T) {
	s, h := newTestService(t)
	mustAdd(t, s, ItemInput{Key: "p", Title: "P"}, ItemInput{Key: "a", Title: "A", Parent: Ref{Key: "p"}},
		ItemInput{Key: "b", Title: "B", Parent: Ref{Key: "p"}})
	out, err := s.SetItemStatus(SetItemStatusRequest{RepoID: repoID, ID: 1, Status: StatusQueued})
	if err != nil {
		t.Fatal(err)
	}
	if out.Item.ID != 1 || !reflect.DeepEqual(h.last(), []int{2, 3}) {
		t.Fatal(out, h.changed)
	}
	if out, err = s.SetItemStatus(SetItemStatusRequest{RepoID: repoID, ID: 2, Status: StatusDone}); err != nil || out.Item.Status != StatusDone {
		t.Fatal(out, err)
	}
	if out, err = s.SetItemStatus(SetItemStatusRequest{RepoID: repoID, ID: 2, Status: StatusDraft}); err != nil || out.Item.Status != StatusDraft {
		t.Fatal(out, err)
	}
	if _, err := s.SetItemStatus(SetItemStatusRequest{RepoID: repoID, ID: 3, Status: StatusReview}); err == nil {
		t.Error("queued to review accepted")
	}
	if _, err := s.SetItemStatus(SetItemStatusRequest{RepoID: repoID, ID: 3, Status: StatusDone, PR: "u"}); err == nil {
		t.Error("pr without session accepted")
	}
	if _, err := s.SetItemStatus(SetItemStatusRequest{RepoID: repoID, ID: 3, Status: StatusReview, SessionID: "s1"}); err == nil ||
		!strings.Contains(err.Error(), "not a run of B-3") {
		t.Error(err)
	}
}

func TestServiceAgentReview(t *testing.T) {
	s, _ := newTestService(t)
	mustAdd(t, s, ItemInput{Key: "a", Title: "A"})
	if _, err := Update(repoID, func(b *Backlog) error {
		it := b.Item(1)
		it.Status = StatusRunning
		it.Runs = []RunRef{{SessionID: "s1", State: RunStarted}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetItemStatus(SetItemStatusRequest{RepoID: repoID, ID: 1, Status: StatusDone, SessionID: "s1"}); err == nil {
		t.Error("agent done accepted")
	}
	out, err := s.SetItemStatus(SetItemStatusRequest{RepoID: repoID, ID: 1, Status: StatusReview, SessionID: "s1", PR: "https://x/pull/1"})
	if err != nil || out.Item.Status != StatusReview || out.Item.PR != "https://x/pull/1" {
		t.Fatal(out, err)
	}
	msg, err := s.PostMessage(PostMessageRequest{RepoID: repoID, ID: 1, Kind: KindNote, Text: "x", SessionID: "s1"})
	if err != nil || msg.Message.Author != "agent:s1" {
		t.Fatal(msg, err)
	}
}

func TestServiceNotes(t *testing.T) {
	s, _ := newTestService(t)
	mustAdd(t, s, ItemInput{Key: "a", Title: "A"})
	for i, text := range []string{"one", " two "} {
		out, err := s.PostMessage(PostMessageRequest{RepoID: repoID, ID: 1, Kind: KindNote, Text: text})
		if err != nil || out.Message.ID != i+1 || out.Message.Author != AuthorUser || out.Message.Text != strings.TrimSpace(text) {
			t.Fatal(out, err)
		}
	}
	for _, req := range []PostMessageRequest{
		{RepoID: repoID, ID: 1, Kind: KindQuestion, Text: "q"},
		{RepoID: repoID, ID: 1, Kind: KindNote, Text: "  "},
		{RepoID: repoID, ID: 1, Kind: KindNote, Text: "x", SessionID: "nope"},
	} {
		if _, err := s.PostMessage(req); err == nil {
			t.Errorf("%+v accepted", req)
		}
	}
}

func TestServiceSettings(t *testing.T) {
	s, _ := newTestService(t)
	out, err := s.SetSettings(SetSettingsRequest{RepoID: repoID, Runner: ptr(true), MaxRunning: ptr(3), OnDone: ptr(OnDoneArchive)})
	if err != nil || out.Settings != (Settings{Runner: true, MaxRunning: 3, OnDone: OnDoneArchive}) {
		t.Fatal(out, err)
	}
	for _, req := range []SetSettingsRequest{
		{RepoID: repoID, MaxRunning: ptr(0)}, {RepoID: repoID, MaxRunning: ptr(9)}, {RepoID: repoID, OnDone: ptr("kill")},
	} {
		if _, err := s.SetSettings(req); err == nil {
			t.Errorf("%+v accepted", req)
		}
	}
	v, _ := s.Get(GetRequest{RepoID: repoID})
	if v.Runner.State != RunnerStopped || v.Runner.Reason != "Set a budget to let the runner start items." || v.Runner.Max != 3 {
		t.Fatalf("%+v", v.Runner)
	}
}

func TestServiceBudget(t *testing.T) {
	s, _ := newTestService(t)
	b, err := s.GetBudget(GetBudgetRequest{})
	if err != nil || b.Budget.Limit != 0 || b.Budget.MaxPerRun != DefaultMaxPerRun {
		t.Fatal(b, err)
	}
	steps := []struct {
		req  SetBudgetRequest
		want func(Budget) bool
	}{
		{SetBudgetRequest{Add: ptr(5.0)}, func(b Budget) bool { return b.Limit == 5 }},
		{SetBudgetRequest{Add: ptr(2.5), MaxPerRun: ptr(1.0)}, func(b Budget) bool { return b.Limit == 7.5 && b.MaxPerRun == 1 }},
		{SetBudgetRequest{Set: ptr(10.0)}, func(b Budget) bool { return b.Limit == 10 }},
		{SetBudgetRequest{MaxPerRun: ptr(0.0)}, func(b Budget) bool { return b.MaxPerRun == 0 && b.Limit == 10 }},
	}
	for _, st := range steps {
		out, err := s.SetBudget(st.req)
		if err != nil || !st.want(out.Budget) {
			t.Fatalf("%+v: %+v %v", st.req, out, err)
		}
	}
	if _, err := UpdateBudget(func(b *Budget) error { b.Spent = 10.4; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSettings(SetSettingsRequest{RepoID: repoID, Runner: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Get(GetRequest{RepoID: repoID})
	if v.Runner.Reason != "Budget used ($10.40 of $10.00)." {
		t.Fatal(v.Runner)
	}
	out, err := s.SetBudget(SetBudgetRequest{Reset: true})
	if err != nil || out.Budget.Spent != 0 || out.Budget.ResetAt.IsZero() {
		t.Fatal(out, err)
	}
	v, _ = s.Get(GetRequest{RepoID: repoID})
	if v.Runner != (RunnerView{State: RunnerIdle, Reason: "Nothing queued.", Max: 1}) {
		t.Fatal(v.Runner)
	}
	for _, req := range []SetBudgetRequest{
		{}, {Add: ptr(1.0), Reset: true}, {Add: ptr(0.0)}, {Set: ptr(-1.0)}, {MaxPerRun: ptr(-1.0)},
	} {
		if _, err := s.SetBudget(req); err == nil {
			t.Errorf("%+v accepted", req)
		}
	}
}
