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
		return backlog.Gate{Name: backlog.GateProgram, Reason: fmt.Sprintf("The budget meters Claude plan use only, and this task runs %s", program)}
	}
	budget, g := loadBudget()
	if !g.Open() {
		return g
	}
	in := m.gateInput(m.claudeAccountFor(t.ProjectPath), budget, m.backlogUsage(), time.Now())
	return backlog.CheckPlanGates(in)
}

// skipOverBudget records a skip and returns true when taskID is budget-gated
// and its gate is closed. A task it cannot load is left to RunTask to report.
func (s *taskScheduler) skipOverBudget(taskID string) bool {
	if s.budgetGate == nil {
		return false
	}
	t, err := task.GetTask(taskID)
	if err != nil || !t.Enabled || !t.BudgetGated {
		return false
	}
	g := s.budgetGate(*t)
	for waited := time.Duration(0); g.Name == backlog.GateMetering && waited < budgetMeterWait; waited += budgetMeterPoll {
		s.sleep(budgetMeterPoll)
		g = s.budgetGate(*t)
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
