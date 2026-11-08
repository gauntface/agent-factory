package daemon

import (
	"fmt"
	"time"

	"github.com/sachiniyer/agent-factory/backlog"
	"github.com/sachiniyer/agent-factory/config"
	"github.com/sachiniyer/agent-factory/log"
	"github.com/sachiniyer/agent-factory/session/tmux"
	"github.com/sachiniyer/agent-factory/task"
)

// TaskStatusSkippedOverBudget prefixes the run status of a budget-gated fire
// the gate held back; the gate's reason follows it.
const TaskStatusSkippedOverBudget = "skipped: over budget"

// taskBudgetGate checks a budget-gated task against the backlog budget's plan
// limit, for the account the task's session would launch with.
func (m *Manager) taskBudgetGate(t task.Task) backlog.Gate {
	program := t.Program
	if program == "" {
		program = defaultProgramFor(m.Config().DefaultProgram, t.ProjectPath)
	}
	if program != tmux.ProgramClaude {
		return backlog.Gate{Name: "program", Reason: fmt.Sprintf("The budget meters Claude plan use only, and this task runs %s", program)}
	}
	budget, err := backlog.LoadBudget()
	if err != nil {
		return backlog.Gate{Name: "budget", Reason: err.Error()}
	}
	repoID := t.RepoID
	if repoID == "" {
		if repo, err := config.RepoFromPath(t.ProjectPath); err == nil {
			repoID = repo.ID
		}
	}
	in, _ := m.gateInput(repoID, *budget, m.backlogUsage(), time.Now())
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
	if g.Open() {
		return false
	}
	status := TaskStatusSkippedOverBudget + " · " + g.Reason
	log.InfoLog.Printf("scheduled task %s skipped: %s", taskID, g.Reason)
	now := time.Now()
	if _, err := task.UpdateTaskStatus(taskID, &now, status); err != nil {
		log.ErrorLog.Printf("failed to record skipped status for task %s: %v", taskID, err)
	}
	return true
}
