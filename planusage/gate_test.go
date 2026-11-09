package planusage

import (
	"testing"
	"time"
)

var gateNow = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func limit(pct float64, resetIn time.Duration) *Limit {
	return &Limit{UsedPercentage: pct, ResetsAt: gateNow.Add(resetIn).Unix()}
}

func gateIn(r *Reading) GateInput {
	return GateInput{Limit: 50, Reading: r, Now: gateNow, Loc: time.UTC}
}

func TestCheckGate(t *testing.T) {
	ok := &Reading{FiveHour: limit(20, 2*time.Hour), SevenDay: limit(30, 96*time.Hour), ReadAt: gateNow.Add(-time.Minute)}
	cases := []struct {
		name   string
		in     GateInput
		gate   string
		reason string
	}{
		{"open", gateIn(ok), "", ""},
		{"limit in the 5-hour window",
			gateIn(&Reading{FiveHour: limit(52, 2*time.Hour), SevenDay: limit(30, 96*time.Hour)}),
			GateLimit, "Plan at 52% of the 5-hour window (limit 50%) · resumes 14:00"},
		{"limit in the week",
			gateIn(&Reading{FiveHour: limit(20, 2*time.Hour), SevenDay: limit(60, 96*time.Hour)}),
			GateLimit, "Plan at 60% of the week (limit 50%) · resumes Tue 12:00"},
		{"limit 0", func() GateInput { in := gateIn(ok); in.Limit = 0; return in }(),
			GateLimit, "Plan limit is 0% · raise it with \"af config set budget_limit 50\""},
		{"usage limit", func() GateInput {
			in := gateIn(ok)
			in.Limited, in.LimitUntil = true, gateNow.Add(3*time.Hour)
			return in
		}(), GateUsageLimit, "Usage limit until 15:00"},
		{"usage limit unknown", func() GateInput { in := gateIn(ok); in.LimitUnknown = "fix the ledger"; return in }(),
			GateUsageLimit, "Can't tell whether the plan is at its usage limit · fix the ledger"},
		{"no reading", gateIn(nil), GateNoReading, "No plan usage seen yet · run a Claude session once to take a reading"},
		{"both windows reset since the last reading",
			gateIn(&Reading{FiveHour: limit(90, -time.Hour), SevenDay: limit(90, -time.Hour)}), "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := CheckGate(c.in)
			if g.Name != c.gate || g.Reason != c.reason {
				t.Fatalf("got %+v, want %s %q", g, c.gate, c.reason)
			}
		})
	}
}
