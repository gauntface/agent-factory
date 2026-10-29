package backlog

import (
	"fmt"
	"maps"
	"time"

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
func MeterRun(id int, project string, r RunRef, files []string, seen map[string]int64, overrides map[string]planusage.Price, now time.Time) RunMeter {
	out := meterFiles(id, project, r, files, planusage.ReadOptions{Overrides: overrides, Seen: seen})
	if len(files) == 0 && now.Sub(r.StartedAt) >= UnmeterableAfter {
		out.Unmeterable = fmt.Sprintf("af cannot find the transcripts of %s's session · %s", ItemInProject(id, project), unmeterableFix(id, project))
	}
	return out
}

// MeterOrchestrator reads the transcripts of group's orchestrator from their
// stored offsets, like MeterRun, and counts only messages from after the
// session was bound.
func MeterOrchestrator(group int, o Orchestrator, files []string, seen map[string]int64, overrides map[string]planusage.Price) RunMeter {
	return meterFiles(group, "", RunRef{Meter: o.Meter}, files, planusage.ReadOptions{Overrides: overrides, Seen: seen, Since: o.BoundAt})
}

func meterFiles(id int, project string, r RunRef, files []string, opts planusage.ReadOptions) RunMeter {
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
			out.Unmeterable = fmt.Sprintf("af cannot read a transcript of %s's session (%s) · %s", ItemInProject(id, project), f, unmeterableFix(id, project))
		case err != nil && out.Unmeterable == "" && res.Mark.Offset == old.Offset:
			out.Unmeterable = fmt.Sprintf("af cannot read a transcript of %s's session: %v", ItemInProject(id, project), err)
		}
	}
	return out
}

// UnmeterableProgram is why a run of a program other than Claude cannot be
// metered.
func UnmeterableProgram(id int, project, program string) string {
	return fmt.Sprintf("the runner can't meter %s, the program of %s · %s", program, ItemInProject(id, project), unmeterableFix(id, project))
}

// ItemInProject names an item for a message that shows outside its project:
// "B-24 in project detail".
func ItemInProject(id int, project string) string {
	if project == "" {
		return FormatID(id)
	}
	return fmt.Sprintf("%s in project %s", FormatID(id), project)
}

func unmeterableFix(id int, project string) string {
	where := ""
	if project != "" {
		where = " in project " + project
	}
	return fmt.Sprintf("run \"af backlog show %d\"%s, then mark %s done or drop it", id, where, FormatID(id))
}

// SetRunLaunchDirs records the launch dir of the runs of the given sessions
// that have none. It returns whether anything changed.
func (b *Backlog) SetRunLaunchDirs(dirs map[string]string) bool {
	changed := false
	for i := range b.Items {
		for j := range b.Items[i].Runs {
			r := &b.Items[i].Runs[j]
			if dir := dirs[r.SessionID]; dir != "" && r.SessionID != "" && r.LaunchDir == "" {
				r.LaunchDir = dir
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
