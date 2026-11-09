package planusage

import "time"

// planWindow is one plan window: the plan percent used and when the window
// resets. resetsAt is nil when there is no valid reading for the window.
type planWindow struct {
	plan     float64
	resetsAt *time.Time
}

func (w planWindow) valid() bool { return w.resetsAt != nil }

// windowOf reads one window of a reading. A window past its reset counts as 0
// until a new reading.
func windowOf(l *Limit, now time.Time) planWindow {
	if l == nil {
		return planWindow{}
	}
	reset := time.Unix(l.ResetsAt, 0).UTC()
	if !now.Before(reset) {
		return planWindow{}
	}
	return planWindow{plan: l.UsedPercentage, resetsAt: &reset}
}
