package planusage

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestBudgetAccounts(t *testing.T) {
	readAt := gateNow.Add(-2 * time.Minute)
	readings := map[string]*Reading{
		"zed":  {FiveHour: limit(62, 2*time.Hour), SevenDay: limit(30, 96*time.Hour), ReadAt: readAt},
		"work": {FiveHour: limit(90, -time.Hour), ReadAt: readAt},
	}
	load := func(a string) (*Reading, error) {
		if a == "broken" {
			return nil, errors.New("bad json")
		}
		return readings[a], nil
	}
	var gotErr error
	gate := func(a string, r *Reading, err error) Gate {
		if err != nil {
			gotErr = err
			return Gate{Name: GateNoReading, Reason: err.Error()}
		}
		return CheckGate(GateInput{Limit: 50, Reading: r, Now: gateNow, Loc: time.UTC})
	}

	out := BudgetAccounts("zed", []string{"work", "default", "zed", "broken", "work"}, gateNow, load, gate)

	var names []string
	for _, a := range out {
		names = append(names, a.Account)
	}
	if want := []string{"zed", "broken", "default", "work"}; !slices.Equal(names, want) {
		t.Fatalf("order: got %v, want %v", names, want)
	}
	zed := out[0]
	if !zed.Default || zed.FiveHour.Plan != 62 || zed.FiveHour.ResetsAt == nil || zed.SevenDay.Plan != 30 {
		t.Fatalf("zed: %+v", zed)
	}
	if zed.ReadingAt == nil || !zed.ReadingAt.Equal(readAt) {
		t.Fatalf("zed reading_at: %v", zed.ReadingAt)
	}
	if zed.Gate.Name != GateLimit || zed.Gate.Reason != "Plan at 62% of the 5-hour window (limit 50%) · resumes 14:00" {
		t.Fatalf("zed gate: %+v", zed.Gate)
	}
	if out[1].Gate.Name != GateNoReading || gotErr == nil || out[1].ReadingAt != nil {
		t.Fatalf("broken: %+v", out[1])
	}
	def := out[2]
	if def.Default || def.ReadingAt != nil || def.FiveHour.ResetsAt != nil || def.Gate.Name != GateNoReading {
		t.Fatalf("default without a reading: %+v", def)
	}
	work := out[3]
	if work.FiveHour.ResetsAt != nil || work.FiveHour.Plan != 0 || !work.Gate.Open() {
		t.Fatalf("work after its reset: %+v", work)
	}
}

func TestBudgetAccountsJSON(t *testing.T) {
	r := &Reading{FiveHour: limit(62, 2*time.Hour), ReadAt: gateNow}
	out := BudgetAccounts(DefaultAccount, nil, gateNow,
		func(string) (*Reading, error) { return r, nil },
		func(string, *Reading, error) Gate { return Gate{} })
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"account":"default","default":true,` +
		`"five_hour":{"plan":62,"resets_at":"2026-11-06T14:00:00Z"},` +
		`"seven_day":{"plan":0,"resets_at":null},` +
		`"reading_at":"2026-11-06T12:00:00Z","gate":{"name":"","reason":""}}]`
	if string(data) != want {
		t.Fatalf("got  %s\nwant %s", data, want)
	}
}
