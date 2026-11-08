package daemon

import (
	"fmt"
	"time"

	"github.com/sachiniyer/agent-factory/backlog"
	"github.com/sachiniyer/agent-factory/log"
	"github.com/sachiniyer/agent-factory/session/tmux"
	"github.com/sachiniyer/agent-factory/task"
)

// TaskStatusSkippedBudgetGate prefixes the run status of a budget-gated fire
// the gate held back; the gate's reason follows it.
const TaskStatusSkippedBudgetGate = "skipped: budget gate"

// gateProgram closes the budget gate of a task whose program resolves to an
// agent other than Claude.
const gateProgram = "program"

// A fire right after a daemon start waits up to budgetMeterWait for the first
// meter pass, which can read a week of transcripts, so that the start alone
// does not cost a nightly task its run.
const (
	budgetMeterWait = 5 * time.Minute
	budgetMeterPoll = 5 * time.Second
)

// taskBudgetGate checks a budget-gated task against the backlog budget's plan
// limit, for the account the task's session would launch with.
func (m *Manager) taskBudgetGate(t task.Task) backlog.Gate {
	program := t.Program
	if program == "" {
		// Validation accepts an empty program, and the default can name another agent.
		program = defaultProgramFor(m.Config().DefaultProgram, t.ProjectPath)
	}
	if program != tmux.ProgramClaude {
		return backlog.Gate{Name: gateProgram, Reason: fmt.Sprintf("The budget meters Claude plan use only, and this task runs %s", program)}
	}
	budget, g := loadBudget()
	if !g.Open() {
		return g
	}
	in := m.gateInput(m.claudeAccountFor(t.ProjectPath), budget, m.backlogUsage(), time.Now())
	return backlog.CheckPlanGates(in)
}

// skipClosedBudgetGate records a skip and returns true when taskID is
// budget-gated and its gate is closed. A task it cannot load is left to
// RunTask to report.
func (s *taskScheduler) skipClosedBudgetGate(taskID string) bool {
	if s.budgetGate == nil {
		return false
	}
	g := s.checkBudgetGate(taskID)
	for waited := time.Duration(0); g.Name == backlog.GateMetering && waited < budgetMeterWait; waited += budgetMeterPoll {
		s.sleep(budgetMeterPoll)
		g = s.checkBudgetGate(taskID)
	}
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

// checkBudgetGate reads the task again on each call, since it may change
// while a fire waits for the first meter pass.
func (s *taskScheduler) checkBudgetGate(taskID string) backlog.Gate {
	t, err := task.GetTask(taskID)
	if err != nil || !t.Enabled || !t.BudgetGated {
		return backlog.Gate{}
	}
	return s.budgetGate(*t)
}
