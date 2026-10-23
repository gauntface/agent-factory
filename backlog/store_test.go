package backlog

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sachiniyer/agent-factory/config"
)

const repoID = "0123456789ab"

func home(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AGENT_FACTORY_HOME", dir)
	return dir
}

func TestPaths(t *testing.T) {
	h := home(t)
	p, err := BacklogPath(repoID)
	if err != nil || p != filepath.Join(h, "repos", repoID, "backlog.json") {
		t.Fatal(p, err)
	}
	for _, bad := range []string{"", "..", "../x", "a/b"} {
		if _, err := BacklogPath(bad); err == nil {
			t.Errorf("repo id %q accepted", bad)
		}
	}
	bp, err := BudgetPath()
	if err != nil || bp != filepath.Join(h, "budget.json") {
		t.Fatal(bp, err)
	}
}

func TestLoadMissing(t *testing.T) {
	home(t)
	b, err := Load(repoID)
	if err != nil {
		t.Fatal(err)
	}
	if b.SchemaVersion != BacklogSchemaVersion || b.NextID != 1 || len(b.Items) != 0 || b.Settings != DefaultSettings() {
		t.Fatalf("%+v", b)
	}
	bud, err := LoadBudget()
	if err != nil || *bud != *NewBudget() || bud.Limit != 50 || bud.MaxPerRun != 10 {
		t.Fatalf("%+v %v", bud, err)
	}
}

func TestUpdateRoundTrip(t *testing.T) {
	home(t)
	_, err := Update(repoID, func(b *Backlog) error {
		_, err := b.Add(NewItem{Title: "one"}, t0)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Update(repoID, func(b *Backlog) error {
		_, err := b.Add(NewItem{Title: "two", WaitsOn: []int{1}}, t0)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Items) != 2 || b.Item(2).WaitsOn[0] != 1 {
		t.Fatalf("%+v", b)
	}
	got, err := Load(repoID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 || got.NextID != 3 || got.Item(2).Title != "two" {
		t.Fatalf("%+v", got)
	}
	path, _ := BacklogPath(repoID)
	raw, _ := os.ReadFile(path)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m["schema_version"] != float64(1) {
		t.Fatalf("%s", raw)
	}
}

func TestUpdateErrorWritesNothing(t *testing.T) {
	home(t)
	boom := errors.New("boom")
	if _, err := Update(repoID, func(*Backlog) error { return boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	path, _ := BacklogPath(repoID)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file written on error")
	}
}

func writeRaw(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRefusesNewerSchema(t *testing.T) {
	home(t)
	path, _ := BacklogPath(repoID)
	newer := `{"schema_version": 2, "next_id": 5, "items": [], "future": true}`
	writeRaw(t, path, newer)

	var unsupported *config.UnsupportedSchemaVersionError
	if _, err := Load(repoID); !errors.As(err, &unsupported) {
		t.Fatalf("load err = %v", err)
	}
	if _, err := Update(repoID, func(*Backlog) error { return nil }); !errors.As(err, &unsupported) || !strings.Contains(err.Error(), "upgrade af") {
		t.Fatalf("update err = %v", err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != newer {
		t.Fatal("newer file overwritten")
	}

	bp, _ := BudgetPath()
	writeRaw(t, bp, `{"schema_version": 9}`)
	if _, err := UpdateBudget(func(b *Budget) error { b.Limit = 1; return nil }); !errors.As(err, &unsupported) {
		t.Fatalf("budget err = %v", err)
	}
}

func TestRefusesUnreadableFile(t *testing.T) {
	home(t)
	path, _ := BacklogPath(repoID)
	for _, bad := range []string{`{"schema_version": "x"}`, `not json`, `{"schema_version": 1, "items": [{"id": 1, "status": "nope"}]}`} {
		writeRaw(t, path, bad)
		if _, err := Update(repoID, func(*Backlog) error { return nil }); err == nil {
			t.Errorf("%s: update accepted", bad)
		}
		if raw, _ := os.ReadFile(path); string(raw) != bad {
			t.Errorf("%s: overwritten", bad)
		}
	}
}

func TestMigratesFileWithoutVersion(t *testing.T) {
	home(t)
	path, _ := BacklogPath(repoID)
	writeRaw(t, path, `{"items": [{"id": 3, "title": "hand", "status": "queued"}]}`)
	b, err := Load(repoID)
	if err != nil {
		t.Fatal(err)
	}
	if b.SchemaVersion != 1 || b.NextID != 4 || b.Item(3).Title != "hand" || b.Settings.MaxRunning != 1 {
		t.Fatalf("%+v", b)
	}
}

func TestLockTimeoutIsBusy(t *testing.T) {
	home(t)
	old := lockTimeout
	lockTimeout = 0
	t.Cleanup(func() { lockTimeout = old })
	path, _ := BacklogPath(repoID)
	if err := config.MkdirAllUnderAFHome(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	err := config.WithFileLock(path, func() error {
		_, err := Update(repoID, func(*Backlog) error { return nil })
		return err
	})
	if !errors.Is(err, ErrBusy) || !errors.Is(err, config.ErrLockTimeout) {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasPrefix(err.Error(), "backlog changed while saving; retry") {
		t.Fatal(err)
	}
}

func TestBudgetRoundTrip(t *testing.T) {
	home(t)
	_, err := UpdateBudget(func(b *Budget) error {
		b.Limit, b.MaxPerRun, b.USDLimit, b.USDSpent = 30, 0, 10, 2.5
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := LoadBudget()
	if err != nil {
		t.Fatal(err)
	}
	want := Budget{SchemaVersion: BudgetSchemaVersion, Limit: 30, MaxPerRun: 0, USDLimit: 10, USDSpent: 2.5}
	if *got != want {
		t.Fatalf("%+v", got)
	}
}

func TestMigratesBudgetToLimit(t *testing.T) {
	cases := []struct {
		name, raw string
		limit     float64
	}{
		{"changed stop_at becomes the limit", `{"schema_version": 1, "share": 100, "stop_at": 50, "max_per_run": 5}`, 50},
		{"a raised stop_at becomes the limit", `{"schema_version": 1, "share": 30, "stop_at": 80, "max_per_run": 5}`, 80},
		{"default stop_at takes the default limit", `{"schema_version": 1, "share": 30, "stop_at": 90, "max_per_run": 5}`, DefaultLimit},
		{"no stop_at takes the default limit", `{"schema_version": 1, "share": 30, "max_per_run": 5}`, DefaultLimit},
		{"no version, old shape", `{"share": 40, "stop_at": 70, "max_per_run": 5}`, 70},
		{"no version, new shape keeps its limit", `{"limit": 35, "max_per_run": 5}`, 35},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home(t)
			bp, _ := BudgetPath()
			writeRaw(t, bp, c.raw)
			got, err := LoadBudget()
			if err != nil {
				t.Fatal(err)
			}
			want := Budget{SchemaVersion: BudgetSchemaVersion, Limit: c.limit, MaxPerRun: 5}
			if *got != want {
				t.Fatalf("%+v", got)
			}
			raw, err := os.ReadFile(bp)
			if err != nil {
				t.Fatal(err)
			}
			var obj map[string]any
			if err := json.Unmarshal(raw, &obj); err != nil {
				t.Fatal(err)
			}
			if obj["schema_version"] != float64(BudgetSchemaVersion) || obj["limit"] != c.limit || obj["share"] != nil || obj["stop_at"] != nil {
				t.Fatalf("file not rewritten in the new shape: %s", raw)
			}
		})
	}
}
