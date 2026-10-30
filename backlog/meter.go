package backlog

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/sachiniyer/agent-factory/config"
	"github.com/sachiniyer/agent-factory/planusage"
)

// UnmeterableAfter is how long a started run may have no transcript before it
// counts as unmeterable.
const UnmeterableAfter = 2 * time.Minute

// RunMeter is one metering of a run's transcripts.
type RunMeter struct {
	Meter   map[string]Meter
	Changed bool
	// Unmeterable says why af cannot meter the run; "" when it can.
	Unmeterable string
	// Entries are the messages this pass counted, for time buckets.
	Entries []planusage.Entry
}

// Cost sums the run's per-file costs.
func (m RunMeter) Cost() float64 { return RunRef{Meter: m.Meter}.Cost() }

// MeterRun reads the run's transcript files from their stored offsets. seen
// holds the keys of the run's messages counted so far, from any of its files,
// and gains what this pass counts; a message Claude Code copied into a new
// transcript (resume, fork) counts once. A run with no files is unmeterable
// once it has run for UnmeterableAfter.
func MeterRun(id int, w Where, r RunRef, files []string, seen map[string]int64, overrides map[string]planusage.Price, now time.Time) RunMeter {
	out := meterFiles(id, w, r, files, planusage.ReadOptions{Overrides: overrides, Seen: seen})
	if len(files) == 0 && now.Sub(r.StartedAt) >= UnmeterableAfter {
		out.Unmeterable = fmt.Sprintf("af cannot find the transcripts of the session of %s · %s", w.Item(id), w.Fix(id))
	}
	return out
}

// MeterOrchestrator reads the transcripts of group's orchestrator from their
// stored offsets, like MeterRun, and counts only messages from after the
// session was bound.
func MeterOrchestrator(group int, o Orchestrator, files []string, seen map[string]int64, overrides map[string]planusage.Price) RunMeter {
	return meterFiles(group, Where{}, RunRef{Meter: o.Meter}, files, planusage.ReadOptions{Overrides: overrides, Seen: seen, Since: o.BoundAt})
}

func meterFiles(id int, w Where, r RunRef, files []string, opts planusage.ReadOptions) RunMeter {
	out := RunMeter{Meter: maps.Clone(r.Meter)}
	if out.Meter == nil {
		out.Meter = map[string]Meter{}
	}
	for _, f := range files {
		old := out.Meter[f]
		res, err := planusage.ReadTranscript(f, planusage.FileMark{Offset: old.Offset, Last: old.Last}, opts)
		out.Entries = append(out.Entries, res.Entries...)
		next := Meter{Offset: res.Mark.Offset, Cost: old.Cost + res.Cost(), Last: res.Mark.Last}
		if _, known := r.Meter[f]; next != old || !known {
			out.Meter[f], out.Changed = next, true
		}
		switch {
		case res.Unpriced != "":
			out.Unmeterable = fmt.Sprintf("no price for model %s; add [backlog.prices.%q] to config.toml", res.Unpriced, res.Unpriced)
		case res.Bad && out.Unmeterable == "":
			out.Unmeterable = fmt.Sprintf("af cannot read a transcript of the session of %s (%s) · %s", w.Item(id), f, w.Fix(id))
		case err != nil && out.Unmeterable == "" && res.Mark.Offset == old.Offset:
			out.Unmeterable = fmt.Sprintf("af cannot read a transcript of the session of %s: %v", w.Item(id), err)
		}
	}
	return out
}

// Where names the project an item is in, for a message shown outside it.
type Where struct {
	// Name is the project's repo name; Root is its root directory.
	Name, Root string
}

// Item names an item: "B-24 in project detail".
func (w Where) Item(id int) string {
	if w.Name == "" {
		return FormatID(id)
	}
	return fmt.Sprintf("%s in project %s", FormatID(id), w.Name)
}

// Fix is what to do about an item af cannot meter: the plain action, then the
// command that opens the item from any directory.
func (w Where) Fix(id int) string {
	out := fmt.Sprintf("mark %s done or drop it", FormatID(id))
	if w.Root != "" {
		return out + fmt.Sprintf(" · see \"af backlog show %d --repo %s\"", id, quoteArg(w.Root))
	}
	return out + fmt.Sprintf(" · see \"af backlog show %d\"", id)
}

// quoteArg quotes a path for a command the user pastes, only when the shell
// would split or interpret it.
func quoteArg(path string) string {
	if strings.Trim(path, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-+:=@%,~") == "" {
		return path
	}
	return config.ShellQuotePath(path)
}

// UnmeterableProgram is why a run of a program other than Claude cannot be
// metered.
func UnmeterableProgram(id int, w Where, program string) string {
	return fmt.Sprintf("the runner can't meter %s, the program of %s · %s", program, w.Item(id), w.Fix(id))
}

// RunBlocksRunner is whether trouble metering a run may stop the runner: only
// a run still at work. An archived session, or a run that ended and was not
// worked in since (pane churn after its end), is metered but never blocks.
func RunBlocksRunner(status Status, archived bool, endedAt, churnAt time.Time) bool {
	if status != StatusRunning || archived {
		return false
	}
	return endedAt.IsZero() || churnAt.After(endedAt)
}

// LaunchDirs are the directories Claude was launched in: the path and what it
// resolved to through symlinks.
type LaunchDirs struct {
	Path, Resolved string
}

// LaunchCandidates are the directories Claude may have run in for r: where
// its session is now unless archived (an archived worktree is not where Claude
// ran), and the recorded launch dirs.
func (r RunRef) LaunchCandidates(current string, archived bool) []string {
	var out []string
	add := func(d string) {
		if d != "" && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	if !archived {
		add(current)
	}
	add(r.LaunchDir)
	add(r.LaunchDirResolved)
	return out
}

// MeteredLocation derives the Claude config dir and transcript dirs from the
// files a run already metered, for a run whose launch dir is unknown.
func (r RunRef) MeteredLocation() (string, []string) {
	var configDir string
	var dirs []string
	for f := range r.Meter {
		dir := filepath.Dir(f)
		if !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
		if parent := filepath.Dir(dir); filepath.Base(parent) == "projects" {
			if cd := filepath.Dir(parent); configDir == "" || cd < configDir {
				configDir = cd
			}
		}
	}
	sort.Strings(dirs)
	return configDir, dirs
}

// WithMeteredFiles adds the transcript files r already metered that still
// exist to files.
func (r RunRef) WithMeteredFiles(files []string) []string {
	for f := range r.Meter {
		if _, err := os.Stat(f); err == nil && !slices.Contains(files, f) {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	return files
}

// SetRunLaunchDirs records the launch dirs of the runs of the given sessions
// that have none. It returns whether anything changed.
func (b *Backlog) SetRunLaunchDirs(dirs map[string]LaunchDirs) bool {
	changed := false
	for i := range b.Items {
		for j := range b.Items[i].Runs {
			r := &b.Items[i].Runs[j]
			if d := dirs[r.SessionID]; d.Path != "" && r.SessionID != "" && r.LaunchDir == "" {
				r.LaunchDir, r.LaunchDirResolved = d.Path, d.Resolved
				changed = true
			}
		}
	}
	return changed
}

// SetRunMeters stores per-file meters on the runs of the given sessions. It
// returns whether anything changed.
func (b *Backlog) SetRunMeters(meters map[string]map[string]Meter) bool {
	changed := false
	for i := range b.Items {
		for j := range b.Items[i].Runs {
			r := &b.Items[i].Runs[j]
			if m, ok := meters[r.SessionID]; ok && r.SessionID != "" && !maps.Equal(r.Meter, m) {
				r.Meter = maps.Clone(m)
				changed = true
			}
		}
	}
	return changed
}
