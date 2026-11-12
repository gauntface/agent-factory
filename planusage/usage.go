package planusage

import "time"

// Window is one plan window: the plan percent used and when the window
// resets. ResetsAt is nil when there is no valid reading for the window.
type Window struct {
	Plan     float64    `json:"plan"`
	ResetsAt *time.Time `json:"resets_at"`
}

func (w Window) valid() bool { return w.ResetsAt != nil }

// windowOf reads one window of a reading. A window past its reset counts as 0
// until a new reading.
func windowOf(l *Limit, now time.Time) Window {
	if l == nil {
		return Window{}
	}
	reset := time.Unix(l.ResetsAt, 0).UTC()
	if !now.Before(reset) {
		return Window{}
	}
	return Window{Plan: l.UsedPercentage, ResetsAt: &reset}
}
