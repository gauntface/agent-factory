package planusage

import "time"

// PlanWindow is one plan window: the plan percent used and when the window
// resets.
type PlanWindow struct {
	Plan float64
	// ResetsAt is nil when there is no valid reading for the window.
	ResetsAt *time.Time
}

// Valid reports whether the window has a reading that has not reset yet.
func (w PlanWindow) Valid() bool { return w.ResetsAt != nil }

type Windows struct {
	FiveHour PlanWindow
	SevenDay PlanWindow
}

// WindowsOf computes the plan windows from a reading. A window past its reset
// counts as 0 until a new reading.
func WindowsOf(r *Reading, now time.Time) Windows {
	if r == nil {
		return Windows{}
	}
	return Windows{FiveHour: windowOf(r.FiveHour, now), SevenDay: windowOf(r.SevenDay, now)}
}

func windowOf(l *Limit, now time.Time) PlanWindow {
	if l == nil {
		return PlanWindow{}
	}
	reset := time.Unix(l.ResetsAt, 0).UTC()
	if !now.Before(reset) {
		return PlanWindow{}
	}
	return PlanWindow{Plan: l.UsedPercentage, ResetsAt: &reset}
}
