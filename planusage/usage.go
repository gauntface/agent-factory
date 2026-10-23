package planusage

import (
	"math"
	"time"
)

// PlanWindow is one plan window: the plan percent used by everything, the
// part attributed to the backlog, and when the window resets.
type PlanWindow struct {
	Plan    float64 `json:"plan"`
	Backlog float64 `json:"backlog"`
	// ResetsAt is nil when there is no valid reading for the window.
	ResetsAt *time.Time `json:"resets_at"`
}

// Valid reports whether the window has a reading that has not reset yet.
func (w PlanWindow) Valid() bool { return w.ResetsAt != nil }

type Windows struct {
	FiveHour PlanWindow `json:"five_hour"`
	SevenDay PlanWindow `json:"seven_day"`
}

// Usage is what the meter knows now.
type Usage struct {
	// Account is the af account the windows are for; "default" is the ambient
	// Claude identity.
	Account string  `json:"account,omitempty"`
	Windows Windows `json:"windows"`
	// ReadingAt is when the newest plan reading was taken; nil when none.
	ReadingAt *time.Time `json:"reading_at"`
	// Runs is each item session's metered cost in US dollars at API prices.
	Runs map[string]float64 `json:"runs"`
	// Unmeterable says, per item session of a running item, why af cannot
	// meter it.
	Unmeterable map[string]string `json:"unmeterable,omitempty"`
}

// SpendEvent is the payload of a backlog.spend event.
type SpendEvent struct {
	FiveHour  PlanWindow         `json:"five_hour"`
	SevenDay  PlanWindow         `json:"seven_day"`
	ReadingAt *time.Time         `json:"reading_at"`
	Runs      map[string]float64 `json:"runs"`
}

func (u Usage) SpendEvent() SpendEvent {
	return SpendEvent{FiveHour: u.Windows.FiveHour, SevenDay: u.Windows.SevenDay, ReadingAt: u.ReadingAt, Runs: u.Runs}
}

// Attribute computes the plan windows from a reading and the account's meter:
// backlog% = plan% × backlog cost / all cost, over the window's span ending at
// its reset. A window past its reset counts as 0 until a new reading.
func Attribute(r *Reading, a *AccountMeter, now time.Time) Windows {
	if r == nil {
		return Windows{}
	}
	return Windows{
		FiveHour: attribute(r.FiveHour, FiveHourSpan, a, now),
		SevenDay: attribute(r.SevenDay, SevenDaySpan, a, now),
	}
}

func attribute(l *Limit, span time.Duration, a *AccountMeter, now time.Time) PlanWindow {
	if l == nil {
		return PlanWindow{}
	}
	reset := time.Unix(l.ResetsAt, 0).UTC()
	if !now.Before(reset) {
		return PlanWindow{}
	}
	w := PlanWindow{Plan: l.UsedPercentage, ResetsAt: &reset}
	all, backlog := a.costIn(reset.Add(-span), reset)
	if all > 0 && backlog > 0 {
		w.Backlog = math.Min(w.Plan, w.Plan*backlog/all)
	}
	return w
}
