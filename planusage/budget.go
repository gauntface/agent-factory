package planusage

import (
	"errors"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sachiniyer/agent-factory/internal/agentaccount"
)

// Window is one plan window as GetBudget reports it. ResetsAt is nil when
// there is no valid reading for the window.
type Window struct {
	Plan     float64    `json:"plan"`
	ResetsAt *time.Time `json:"resets_at"`
}

// WindowAt reports one window of a reading at now; a window past its reset
// reads as no reading.
func WindowAt(l *Limit, now time.Time) Window {
	w := windowOf(l, now)
	return Window{Plan: w.plan, ResetsAt: w.resetsAt}
}

// AccountBudget is one Claude account's plan use and the budget gate it
// gives a budget-gated task.
type AccountBudget struct {
	Account string `json:"account"`
	// Default is true for the account new Claude sessions launch with.
	Default   bool       `json:"default"`
	FiveHour  Window     `json:"five_hour"`
	SevenDay  Window     `json:"seven_day"`
	ReadingAt *time.Time `json:"reading_at"`
	Gate      Gate       `json:"gate"`
}

// ReadingAccounts names the accounts that have a reading file in Dir.
func ReadingAccounts() ([]string, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !e.Type().IsRegular() {
			continue
		}
		if name != DefaultAccount && agentaccount.ValidateName(name) != nil {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

// BudgetAccounts reports the default account and every other named account,
// default first, then by name. load reads an account's reading; gate checks
// it, and gets the load error so it can report it.
func BudgetAccounts(defaultAccount string, accounts []string, now time.Time,
	load func(account string) (*Reading, error),
	gate func(account string, r *Reading, err error) Gate,
) []AccountBudget {
	seen := map[string]bool{defaultAccount: true}
	names := []string{}
	for _, a := range accounts {
		if !seen[a] {
			seen[a] = true
			names = append(names, a)
		}
	}
	sort.Strings(names)
	names = append([]string{defaultAccount}, names...)
	out := make([]AccountBudget, 0, len(names))
	for _, name := range names {
		r, err := load(name)
		b := AccountBudget{Account: name, Default: name == defaultAccount, Gate: gate(name, r, err)}
		if err == nil && r != nil {
			b.FiveHour, b.SevenDay = WindowAt(r.FiveHour, now), WindowAt(r.SevenDay, now)
			if !r.ReadAt.IsZero() {
				at := r.ReadAt
				b.ReadingAt = &at
			}
		}
		out = append(out, b)
	}
	return out
}
