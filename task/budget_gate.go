package task

import (
	"fmt"

	"github.com/sachiniyer/agent-factory/session/tmux"
)

func (t Task) runsClaude() bool {
	return t.Program == "" || t.Program == tmux.ProgramClaude
}

func (t Task) validateBudgetGate() error {
	if !t.BudgetGated {
		return nil
	}
	if t.IsWatch() {
		return fmt.Errorf("task %s sets budget_gated but is a watch task; the budget gate applies to scheduled cron fires only", t.ID)
	}
	if !t.runsClaude() {
		return fmt.Errorf("task %s sets budget_gated with program %q; the budget meters Claude plan use only, so use program %q or drop budget_gated", t.ID, t.Program, tmux.ProgramClaude)
	}
	return nil
}

// clearInapplicableBudgetGate drops the budget gate from a task that became a
// watch task or moved to a program other than Claude.
func (t *Task) clearInapplicableBudgetGate() {
	if t.IsWatch() || !t.runsClaude() {
		t.BudgetGated = false
	}
}
