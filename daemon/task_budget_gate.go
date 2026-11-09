package daemon

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sachiniyer/agent-factory/config"
	"github.com/sachiniyer/agent-factory/internal/agentaccount"
	"github.com/sachiniyer/agent-factory/log"
	"github.com/sachiniyer/agent-factory/planusage"
	"github.com/sachiniyer/agent-factory/session"
	"github.com/sachiniyer/agent-factory/session/tmux"
	"github.com/sachiniyer/agent-factory/task"
)

// TaskStatusSkippedBudgetGate prefixes the run status of a budget-gated fire
// the gate held back; the gate's reason follows it.
const TaskStatusSkippedBudgetGate = "skipped: budget gate"

// gateProgram closes the budget gate of a task whose program resolves to an
// agent other than Claude.
const gateProgram = "program"

// ledgerWarned is set while the account limit ledger fails to read, so the
// gate logs once per run of failures.
var ledgerWarned atomic.Bool

// taskBudgetGate checks a budget-gated task against budget_limit, for the
// account the task's session would launch with.
func (m *Manager) taskBudgetGate(t task.Task) planusage.Gate {
	cfg := m.Config()
	program := t.Program
	if program == "" {
		// Validation accepts an empty program, and the default can name another agent.
		program = defaultProgramFor(cfg.DefaultProgram, t.ProjectPath)
	}
	if program != tmux.ProgramClaude {
		return planusage.Gate{Name: gateProgram, Reason: fmt.Sprintf("The budget gate reads Claude plan use only, and this task runs %s", program)}
	}
	account := m.claudeAccountFor(t.ProjectPath)
	reading, err := planusage.LoadReading(account)
	if err != nil {
		return planusage.Gate{Name: planusage.GateNoReading, Reason: "Can't read the plan usage · " + err.Error()}
	}
	now := time.Now()
	limited, until, limitErr := m.claudeUsageLimit(account, now)
	in := planusage.GateInput{
		Limit: float64(cfg.BudgetLimit), Reading: reading,
		Limited: limited, LimitUntil: until,
		Now: now, Loc: time.Local,
	}
	if limitErr != nil {
		in.LimitUnknown = limitErr.Error()
	}
	return planusage.CheckGate(in)
}

// skipClosedBudgetGate records a skip and returns true when taskID is
// budget-gated and its gate is closed. A task it cannot load is left to
// RunTask to report.
func (s *taskScheduler) skipClosedBudgetGate(taskID string) bool {
	if s.budgetGate == nil {
		return false
	}
	t, err := task.GetTask(taskID)
	if err != nil || !t.Enabled || !t.BudgetGated {
		return false
	}
	g := s.budgetGate(*t)
	if g.Open() {
		return false
	}
	log.InfoLog.Printf("scheduled task %s skipped: %s", taskID, g.Reason)
	now := time.Now()
	if _, err := task.UpdateTaskStatus(taskID, &now, TaskStatusSkippedBudgetGate+" · "+g.Reason); err != nil {
		log.ErrorLog.Printf("failed to record skipped status for task %s: %v", taskID, err)
	}
	return true
}

// claudeAccountFor names the Claude account a new session in the repo at path
// launches with when no account is asked for.
func (m *Manager) claudeAccountFor(path string) string {
	project, global := config.DefaultAccountLayersFor(m.Config(), path, tmux.ProgramClaude)
	return planAccount(agentaccount.Resolve("", project.Name, global.Name))
}

// claudeUsageLimit reports whether the Claude account has a usage limit that
// has not reset, from live sessions and the account limit ledger, and the
// latest known reset. An unreadable ledger is an error: the gate stays closed
// rather than run into a limit it cannot see.
func (m *Manager) claudeUsageLimit(account string, now time.Time) (bool, time.Time, error) {
	var obs []session.AccountLimitObservationData
	m.mu.Lock()
	for _, inst := range m.instances {
		if inst == nil {
			continue
		}
		if agent, acct, ok := inst.LimitIdentity(); ok {
			at, _ := inst.LimitResetAt()
			obs = append(obs, session.AccountLimitObservationData{Agent: agent, Account: acct, ResetAt: at})
		}
		obs = append(obs, inst.AccountLimitObservations()...)
	}
	m.mu.Unlock()
	ledger, err := loadAccountLimitLedger()
	warn := !ledgerWarned.Swap(err != nil) && err != nil
	if err != nil {
		if warn {
			m.warn().Printf("budget gate: account limit ledger: %v", err)
		}
		path, _ := accountLimitLedgerPath()
		return false, time.Time{}, fmt.Errorf("af cannot read %s; fix or remove it", path)
	}
	limited, until := activeLimit(append(obs, ledger...), tmux.ProgramClaude, account, now)
	return limited, until, nil
}

func activeLimit(obs []session.AccountLimitObservationData, agent, account string, now time.Time) (bool, time.Time) {
	limited := false
	var until time.Time
	for _, o := range obs {
		if o.Agent != agent || planAccount(o.Account) != account {
			continue
		}
		if !o.ResetAt.IsZero() && !now.Before(o.ResetAt.Add(limitResumeGrace)) {
			continue
		}
		limited = true
		if o.ResetAt.After(until) {
			until = o.ResetAt
		}
	}
	return limited, until
}

// planAccount names an af account the way plan readings do: the ambient
// identity is "default".
func planAccount(name string) string {
	if name = strings.TrimSpace(name); name == "" {
		return planusage.DefaultAccount
	}
	return name
}
