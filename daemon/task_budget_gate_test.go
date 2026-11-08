package daemon

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sachiniyer/agent-factory/backlog"
	"github.com/sachiniyer/agent-factory/task"
)

func TestSkipOverBudget(t *testing.T) {
	t.Setenv("AGENT_FACTORY_HOME", t.TempDir())
	require.NoError(t, task.AddTask(task.Task{ID: "gated", Name: "g", CronExpr: "0 9 * * *", Prompt: "p", Enabled: true, BudgetGated: true}))
	require.NoError(t, task.AddTask(task.Task{ID: "plain", Name: "p", CronExpr: "0 9 * * *", Prompt: "p", Enabled: true}))

	gate := backlog.Gate{}
	calls := 0
	s := newTaskScheduler()
	s.budgetGate = func(task.Task) backlog.Gate {
		calls++
		return gate
	}

	assert.False(t, s.skipOverBudget("plain"))
	assert.Equal(t, 0, calls, "a task without the gate must not read the budget")

	assert.False(t, s.skipOverBudget("gated"), "an open gate lets the task run")

	gate = backlog.Gate{Name: backlog.GateLimit, Reason: "Plan at 60% of the week (limit 50%) · resumes Mon 12:00"}
	assert.True(t, s.skipOverBudget("gated"))
	got, err := task.GetTask("gated")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(got.LastRunStatus, TaskStatusSkippedOverBudget), got.LastRunStatus)
	assert.Contains(t, got.LastRunStatus, "Plan at 60% of the week")
	assert.NotNil(t, got.LastRunAt)
}
