package planusage

import (
	"sort"
	"time"
)

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
			b.FiveHour, b.SevenDay = windowOf(r.FiveHour, now), windowOf(r.SevenDay, now)
			if !r.ReadAt.IsZero() {
				at := r.ReadAt
				b.ReadingAt = &at
			}
		}
		out = append(out, b)
	}
	return out
}
