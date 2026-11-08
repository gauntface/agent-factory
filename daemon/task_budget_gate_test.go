package daemon

import (
	"strings"
	"testing"
	"time"

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
	assert.True(t, strings.HasPrefix(got.LastRunStatus, TaskStatusSkippedBudgetGate), got.LastRunStatus)
	assert.Contains(t, got.LastRunStatus, "Plan at 60% of the week")
	assert.NotNil(t, got.LastRunAt)
}

func TestSkipOverBudgetWaitsForTheFirstMeterPass(t *testing.T) {
	t.Setenv("AGENT_FACTORY_HOME", t.TempDir())
	require.NoError(t, task.AddTask(task.Task{ID: "gated", Name: "g", CronExpr: "0 9 * * *", Prompt: "p", Enabled: true, BudgetGated: true}))

	s := newTaskScheduler()
	var slept time.Duration
	s.sleep = func(d time.Duration) { slept += d }
	calls := 0
	s.budgetGate = func(task.Task) backlog.Gate {
		calls++
		if calls < 3 {
			return backlog.Gate{Name: backlog.GateMetering, Reason: "Reading plan usage…"}
		}
		return backlog.Gate{}
	}
	assert.False(t, s.skipOverBudget("gated"), "the task runs once the first pass shows room")
	assert.Equal(t, 2*budgetMeterPoll, slept)

	calls, slept = -1000, 0
	assert.True(t, s.skipOverBudget("gated"), "a pass that never ends still skips the fire")
	assert.Equal(t, budgetMeterWait, slept)
	got, err := task.GetTask("gated")
	require.NoError(t, err)
	assert.Contains(t, got.LastRunStatus, "Reading plan usage…")
}
