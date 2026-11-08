package backlog

import (
	"testing"
	"time"

	"github.com/sachiniyer/agent-factory/planusage"
)

func TestCheckPlanGates(t *testing.T) {
	ok := readUsage(planWindow(20, 2*time.Hour), planWindow(30, 4*24*time.Hour), time.Minute)
	cases := []struct {
		name   string
		in     func() GateInput
		gate   string
		reason string
	}{
		{"open", func() GateInput { return gateIn(ok) }, "", ""},
		{"limit in the 5-hour window", func() GateInput {
			return gateIn(readUsage(planWindow(52, 2*time.Hour), planWindow(30, 96*time.Hour), 0))
		}, GateLimit, "Plan at 52% of the 5-hour window (limit 50%) · resumes 14:00"},
		{"limit in the week", func() GateInput {
			return gateIn(readUsage(planWindow(20, 2*time.Hour), planWindow(60, 96*time.Hour), 0))
		}, GateLimit, "Plan at 60% of the week (limit 50%) · resumes Mon 12:00"},
		{"limit 0", func() GateInput {
			in := gateIn(ok)
			in.Budget.Limit = 0
			return in
		}, GateLimit, "Plan limit is 0% · raise it with \"af backlog budget --limit 50\""},
		{"usage limit", func() GateInput {
			in := gateIn(ok)
			in.Limited, in.LimitUntil = true, runnerNow.Add(3*time.Hour)
			return in
		}, GateUsageLimit, "Usage limit until 15:00"},
		{"account unknown", func() GateInput {
			in := gateIn(ok)
			in.NoAccount = "af cannot tell which account"
			return in
		}, GateNoAccount, "Af cannot tell which account"},
		{"no reading, even with a dollar limit", func() GateInput {
			in := gateIn(planusage.Usage{})
			in.Budget.USDLimit = 10
			return in
		}, GateNoReading, "No plan usage seen yet · run a Claude session once to take a reading"},
		{"both windows reset since the last reading", func() GateInput {
			return gateIn(readUsage(planusage.PlanWindow{}, planusage.PlanWindow{}, 8*24*time.Hour))
		}, "", ""},
		{"first meter pass not done", func() GateInput { return gateIn(planusage.Usage{Metering: true}) }, GateMetering, "Reading plan usage…"},
		{"a later meter pass keeps the last reading", func() GateInput {
			u := ok
			u.Metering = true
			return gateIn(u)
		}, "", ""},
		{"backlog dollar spend does not hold a task", func() GateInput {
			u := ok
			u.Windows.FiveHour.BacklogCost = 50
			in := gateIn(u)
			in.Budget.USDLimit = 10
			return in
		}, "", ""},
		{"an unmeterable backlog run does not hold a task", func() GateInput {
			u := ok
			u.Unmeterable = map[string]string{"s1": "no price for model x"}
			return gateIn(u)
		}, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := CheckPlanGates(c.in())
			if g.Name != c.gate || g.Reason != c.reason {
				t.Fatalf("got %+v, want %s %q", g, c.gate, c.reason)
			}
		})
	}
}
