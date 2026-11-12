package daemon

import (
	"time"

	"github.com/sachiniyer/agent-factory/planusage"
)

// GetBudgetRequest reads the Claude plan budget. It takes no arguments.
type GetBudgetRequest struct{}

// GetBudgetResponse is budget_limit and each Claude account's plan use, with
// the gate a budget-gated task meets on that account now.
type GetBudgetResponse struct {
	Limit    int                       `json:"limit"`
	Accounts []planusage.AccountBudget `json:"accounts"`
}

// GetBudget reports the plan budget. A read, so no mutation admission gate.
func (s *controlServer) GetBudget(req GetBudgetRequest, resp *GetBudgetResponse) error {
	out, err := s.manager.GetBudget()
	if err != nil {
		return err
	}
	*resp = out
	return nil
}

// GetBudget lists the default Claude account and every account with a plan
// reading. The gate is the one taskBudgetGate gives a Claude task.
func (m *Manager) GetBudget() (GetBudgetResponse, error) {
	accounts, err := planusage.ReadingAccounts()
	if err != nil {
		return GetBudgetResponse{}, err
	}
	limit := m.Config().BudgetLimit
	now := time.Now()
	gate := func(account string, r *planusage.Reading, readErr error) planusage.Gate {
		return m.planGate(account, r, readErr, limit, now)
	}
	return GetBudgetResponse{
		Limit:    limit,
		Accounts: planusage.BudgetAccounts(m.claudeAccountFor(""), accounts, now, planusage.LoadReading, gate),
	}, nil
}
