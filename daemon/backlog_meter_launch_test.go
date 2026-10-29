package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sachiniyer/agent-factory/backlog"
)

func TestRunLaunchDirs(t *testing.T) {
	run := backlog.RunRef{LaunchDir: "/wt/original"}
	cases := []struct {
		name     string
		run      backlog.RunRef
		current  string
		archived bool
		want     []string
	}{
		{"live, never recorded", backlog.RunRef{}, "/wt/now", false, []string{"/wt/now"}},
		{"live in the launch dir", run, "/wt/original", false, []string{"/wt/original"}},
		{"restored elsewhere", run, "/wt/restored", false, []string{"/wt/restored", "/wt/original"}},
		{"archived", run, "/archived/x", true, []string{"/wt/original"}},
		{"archived, never recorded", backlog.RunRef{}, "/archived/x", true, nil},
	}
	for _, c := range cases {
		if got := runLaunchDirs(c.run, c.current, c.archived); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestWithMeteredFilesKeepsOnlyExistingFiles(t *testing.T) {
	dir := t.TempDir()
	kept := filepath.Join(dir, "kept.jsonl")
	if err := os.WriteFile(kept, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	run := backlog.RunRef{Meter: map[string]backlog.Meter{kept: {}, filepath.Join(dir, "gone.jsonl"): {}}}
	if got := withMeteredFiles(nil, run); !slices.Equal(got, []string{kept}) {
		t.Fatalf("got %v", got)
	}
	if got := withMeteredFiles([]string{kept}, run); !slices.Equal(got, []string{kept}) {
		t.Fatalf("duplicate: %v", got)
	}
}
