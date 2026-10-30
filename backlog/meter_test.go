package backlog

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sachiniyer/agent-factory/planusage"
)

func transcriptLine(id, model string, in int) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":"2026-10-20T10:00:00Z","message":{"id":%q,"model":%q,"usage":{"input_tokens":%d,"output_tokens":0}}}`+"\n", id, model, in)
}

func writeFile(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "")), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMeterRunAcrossPassesAndSubagents(t *testing.T) {
	project := filepath.Join(t.TempDir(), "projects", "-wt")
	main := filepath.Join(project, "s.jsonl")
	sub := filepath.Join(project, "s", "subagents", "agent-1.jsonl")
	writeFile(t, main, transcriptLine("m1", "claude-opus-5-5", 1_000_000), transcriptLine("m1", "claude-opus-5-5", 1_000_000))
	writeFile(t, sub, transcriptLine("m2", "claude-sonnet-5-5", 1_000_000))
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	run := RunRef{SessionID: "s1", StartedAt: now.Add(-time.Hour)}
	files := planusage.TranscriptFiles([]string{project})

	m := MeterRun(3, Where{}, run, files, map[string]int64{}, nil, now)
	if !m.Changed || m.Unmeterable != "" || math.Abs(m.Cost()-6) > 1e-9 || len(m.Meter) != 2 || len(m.Entries) != 2 {
		t.Fatalf("%+v", m)
	}
	run.Meter = m.Meter
	again := MeterRun(3, Where{}, run, files, map[string]int64{}, nil, now)
	if again.Changed || math.Abs(again.Cost()-6) > 1e-9 || len(again.Entries) != 0 {
		t.Fatalf("a second pass counted again: %+v", again)
	}

	b := New()
	b.Items = []Item{{ID: 3, Status: StatusRunning, Runs: []RunRef{{SessionID: "s1"}, {SessionID: "s2"}}}}
	if !b.SetRunMeters(map[string]map[string]Meter{"s1": m.Meter}) || b.SetRunMeters(map[string]map[string]Meter{"s1": m.Meter}) {
		t.Fatal("SetRunMeters change report")
	}
	if b.Items[0].Runs[0].Cost() != m.Cost() || b.Items[0].Runs[1].Meter != nil {
		t.Fatalf("%+v", b.Items[0].Runs)
	}
}

func TestMeterRunUnmeterable(t *testing.T) {
	where := Where{Name: "sahara", Root: "/src/sahara"}
	project := filepath.Join(t.TempDir(), "projects", "-wt")
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	young := RunRef{StartedAt: now.Add(-time.Minute)}
	if m := MeterRun(12, where, young, nil, nil, nil, now); m.Unmeterable != "" {
		t.Fatalf("a young run with no files: %q", m.Unmeterable)
	}
	old := RunRef{StartedAt: now.Add(-3 * time.Minute)}
	if m := MeterRun(12, where, old, nil, nil, nil, now); !strings.Contains(m.Unmeterable, "cannot find the transcripts of the session of B-12 in project sahara") ||
		!strings.Contains(m.Unmeterable, `mark B-12 done or drop it · see "af backlog show 12 --repo /src/sahara"`) {
		t.Fatalf("%q", m.Unmeterable)
	}
	f := filepath.Join(project, "s.jsonl")
	writeFile(t, f, transcriptLine("m1", "claude-x-9", 5))
	m := MeterRun(12, where, old, []string{f}, nil, nil, now)
	if m.Unmeterable != `no price for model claude-x-9; add [backlog.prices."claude-x-9"] to config.toml` || m.Cost() != 0 {
		t.Fatalf("%+v", m)
	}
	priced := MeterRun(12, where, old, []string{f}, nil, map[string]planusage.Price{"claude-x-9": {Input: 1e6}}, now)
	if priced.Unmeterable != "" || math.Abs(priced.Cost()-5) > 1e-9 {
		t.Fatalf("%+v", priced)
	}
	if got := UnmeterableProgram(12, where, "aider"); got != `the runner can't meter aider, the program of B-12 in project sahara · mark B-12 done or drop it · see "af backlog show 12 --repo /src/sahara"` {
		t.Fatal(got)
	}
}

func TestMeterRunCountsAResumedTranscriptsCopiesOnce(t *testing.T) {
	project := filepath.Join(t.TempDir(), "projects", "-wt")
	first := filepath.Join(project, "a.jsonl")
	writeFile(t, first, transcriptLine("m1", "claude-opus-5-5", 1_000_000))
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	run := RunRef{SessionID: "s1", StartedAt: now.Add(-time.Hour)}
	seen := map[string]int64{}
	m := MeterRun(3, Where{}, run, []string{first}, seen, nil, now)
	run.Meter = m.Meter

	resumed := filepath.Join(project, "b.jsonl")
	writeFile(t, resumed, transcriptLine("m1", "claude-opus-5-5", 1_000_000), transcriptLine("m2", "claude-opus-5-5", 1_000_000))
	again := MeterRun(3, Where{}, run, []string{first, resumed}, seen, nil, now)
	if math.Abs(again.Cost()-8) > 1e-9 {
		t.Fatalf("cost %v, want 8: the copy of m1 counted again", again.Cost())
	}
}

func TestMeterOrchestratorCountsFromBind(t *testing.T) {
	f := filepath.Join(t.TempDir(), "projects", "-repo", "c1.jsonl")
	early := strings.Replace(transcriptLine("m0", "claude-opus-5-5", 1_000_000), "10:00:00", "09:00:00", 1)
	writeFile(t, f, early, transcriptLine("m1", "claude-opus-5-5", 1_000_000))
	o := Orchestrator{SessionID: "o", BoundAt: time.Date(2026, 10, 20, 9, 30, 0, 0, time.UTC)}
	m := MeterOrchestrator(1, o, []string{f}, map[string]int64{}, nil)
	if !m.Changed || m.Unmeterable != "" || len(m.Entries) != 1 || math.Abs(m.Cost()-4) > 1e-9 {
		t.Fatalf("%+v", m)
	}
	if m := MeterOrchestrator(1, o, nil, nil, nil); m.Unmeterable != "" || m.Changed {
		t.Fatalf("no conversation yet: %+v", m)
	}
}

func TestSetRunLaunchDirsKeepsTheFirst(t *testing.T) {
	b := &Backlog{Items: []Item{{ID: 1, Runs: []RunRef{{SessionID: "s1"}, {SessionID: "s2", LaunchDir: "/wt/two"}}}}}
	if !b.SetRunLaunchDirs(map[string]LaunchDirs{"s1": {Path: "/wt/one", Resolved: "/real/one"}, "s2": {Path: "/elsewhere"}, "": {Path: "/x"}}) {
		t.Fatal("expected a change")
	}
	runs := b.Items[0].Runs
	if runs[0].LaunchDir != "/wt/one" || runs[0].LaunchDirResolved != "/real/one" || runs[1].LaunchDir != "/wt/two" {
		t.Fatalf("%+v", runs)
	}
	if b.SetRunLaunchDirs(map[string]LaunchDirs{"s1": {Path: "/wt/other"}}) {
		t.Fatal("a recorded launch dir changed")
	}
}

func TestWhere(t *testing.T) {
	if got := (Where{}).Item(3); got != "B-3" {
		t.Fatal(got)
	}
	if got := (Where{}).Fix(3); got != `mark B-3 done or drop it · see "af backlog show 3"` {
		t.Fatal(got)
	}
	if got := (Where{Name: "detail"}).Item(3); got != "B-3 in project detail" {
		t.Fatal(got)
	}
}

func TestWhereFixQuotesTheRepoPath(t *testing.T) {
	cases := map[string]string{
		"/src/sahara":     `/src/sahara`,
		"/src/my project": `'/src/my project'`,
		"/src/it's$(x)":   `'/src/it'\''s$(x)'`,
		"/src/a;rm -rf /": `'/src/a;rm -rf /'`,
	}
	for root, want := range cases {
		got := (Where{Root: root}).Fix(7)
		if wantFix := `mark B-7 done or drop it · see "af backlog show 7 --repo ` + want + `"`; got != wantFix {
			t.Errorf("%s: got %s, want %s", root, got, wantFix)
		}
	}
}

func TestRunBlocksRunner(t *testing.T) {
	end := time.Date(2026, 10, 23, 15, 17, 0, 0, time.UTC)
	cases := []struct {
		name     string
		status   Status
		archived bool
		ended    time.Time
		churn    time.Time
		want     bool
	}{
		{"running and live", StatusRunning, false, time.Time{}, time.Time{}, true},
		{"archived", StatusRunning, true, time.Time{}, time.Time{}, false},
		{"ended", StatusRunning, false, end, end.Add(-time.Second), false},
		{"ended, then worked in", StatusRunning, false, end, end.Add(time.Second), true},
		{"ended then re-armed by a new run", StatusRunning, false, time.Time{}, end, true},
		{"review", StatusReview, false, time.Time{}, time.Time{}, false},
		{"not Claude, still running", StatusRunning, false, time.Time{}, time.Time{}, true},
	}
	for _, c := range cases {
		if got := RunBlocksRunner(c.status, c.archived, c.ended, c.churn); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestLaunchCandidates(t *testing.T) {
	run := RunRef{LaunchDir: "/wt/original", LaunchDirResolved: "/real/original"}
	cases := []struct {
		name     string
		run      RunRef
		current  string
		archived bool
		want     []string
	}{
		{"live, never recorded", RunRef{}, "/wt/now", false, []string{"/wt/now"}},
		{"live in the launch dir", RunRef{LaunchDir: "/wt/now"}, "/wt/now", false, []string{"/wt/now"}},
		{"restored elsewhere", run, "/wt/restored", false, []string{"/wt/restored", "/wt/original", "/real/original"}},
		{"archived", run, "/archived/x", true, []string{"/wt/original", "/real/original"}},
		{"archived, never recorded", RunRef{}, "/archived/x", true, nil},
	}
	for _, c := range cases {
		if got := c.run.LaunchCandidates(c.current, c.archived); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMeteredLocation(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "acct")
	dir := filepath.Join(cfg, "projects", "-wt")
	f := filepath.Join(dir, "s.jsonl")
	run := RunRef{Meter: map[string]Meter{f: {}}}
	gotCfg, dirs := run.MeteredLocation()
	if gotCfg != cfg || !slices.Equal(dirs, []string{dir}) {
		t.Fatalf("%q %v", gotCfg, dirs)
	}
	if c, d := (RunRef{}).MeteredLocation(); c != "" || len(d) != 0 {
		t.Fatalf("%q %v", c, d)
	}
}

func TestWithMeteredFilesKeepsOnlyExistingFiles(t *testing.T) {
	dir := t.TempDir()
	kept := filepath.Join(dir, "kept.jsonl")
	writeFile(t, kept, "")
	run := RunRef{Meter: map[string]Meter{kept: {}, filepath.Join(dir, "gone.jsonl"): {}}}
	if got := run.WithMeteredFiles(nil); !slices.Equal(got, []string{kept}) {
		t.Fatalf("got %v", got)
	}
	if got := run.WithMeteredFiles([]string{kept}); !slices.Equal(got, []string{kept}) {
		t.Fatalf("duplicate: %v", got)
	}
}
