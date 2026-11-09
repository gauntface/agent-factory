package planusage

import (
	"fmt"
	"strings"
	"time"
)

// Gate names for budget-gated tasks.
const (
	GateLimit      = "limit"
	GateUsageLimit = "usage_limit"
	GateNoReading  = "no_reading"
	// GateProgram closes the gate of a task whose program is not Claude.
	GateProgram = "program"
)

// GateInput is what the plan gate reads for one Claude account.
type GateInput struct {
	// Limit is the plan percent under which the gate is open, in both windows.
	Limit   float64
	Reading *Reading
	Limited bool
	// LimitUntil is when the usage limit resets; zero when unknown.
	LimitUntil time.Time
	// LimitUnknown says why af cannot tell whether the account is at its usage
	// limit; the gate then stays closed.
	LimitUnknown string
	Now          time.Time
	// Loc is the zone reset times are shown in.
	Loc *time.Location
}

// Gate is a closed gate: its name and the reason the UI shows. The zero Gate
// is open.
type Gate struct {
	Name   string
	Reason string
}

func (g Gate) Open() bool { return g.Name == "" }

// CheckGate reports the first closed gate, in the order limit 0, plan window
// at the limit, usage limit (or unknown), no reading.
func CheckGate(in GateInput) Gate {
	if in.Limit <= 0 {
		return Gate{GateLimit, "Plan limit is 0% · raise it with \"af config set budget_limit 50\""}
	}
	var fiveHour, sevenDay planWindow
	if in.Reading != nil {
		fiveHour, sevenDay = windowOf(in.Reading.FiveHour, in.Now), windowOf(in.Reading.SevenDay, in.Now)
	}
	type labelled struct {
		w     planWindow
		label string
	}
	// When both windows are over, report the later reset: that is when the
	// gate opens.
	var over *labelled
	for _, x := range []labelled{{fiveHour, "the 5-hour window"}, {sevenDay, "the week"}} {
		if x.w.valid() && x.w.plan >= in.Limit && (over == nil || x.w.resetsAt.After(*over.w.resetsAt)) {
			over = &x
		}
	}
	if over != nil {
		return Gate{GateLimit, fmt.Sprintf("Plan at %s of %s (limit %s) · resumes %s",
			percent(over.w.plan), over.label, percent(in.Limit), resetLabel(*over.w.resetsAt, in.Now, in.Loc))}
	}
	if in.LimitUnknown != "" {
		return Gate{GateUsageLimit, "Can't tell whether the plan is at its usage limit · " + in.LimitUnknown}
	}
	if in.Limited {
		reason := "Usage limit"
		if !in.LimitUntil.IsZero() {
			reason += " until " + resetLabel(in.LimitUntil, in.Now, in.Loc)
		}
		return Gate{GateUsageLimit, reason}
	}
	if in.Reading == nil {
		return Gate{GateNoReading, "No plan usage seen yet · run a Claude session once to take a reading"}
	}
	return Gate{}
}

// percent formats a percent: 50%, 12.5%.
func percent(v float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.1f", v), "0"), ".") + "%"
}

// resetLabel shows a reset time: 14:00 within a day, else Mon 09:00.
func resetLabel(at, now time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	at = at.In(loc)
	if at.Sub(now) < 24*time.Hour {
		return at.Format("15:04")
	}
	return at.Format("Mon 15:04")
}
