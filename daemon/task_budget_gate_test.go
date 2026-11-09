package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sachiniyer/agent-factory/planusage"
	"github.com/sachiniyer/agent-factory/session"
	"github.com/sachiniyer/agent-factory/task"
)

func TestSkipOverBudget(t *testing.T) {
	t.Setenv("AGENT_FACTORY_HOME", t.TempDir())
	require.NoError(t, task.AddTask(task.Task{ID: "gated", Name: "g", CronExpr: "0 9 * * *", Prompt: "p", Enabled: true, BudgetGated: true}))
	require.NoError(t, task.AddTask(task.Task{ID: "plain", Name: "p", CronExpr: "0 9 * * *", Prompt: "p", Enabled: true}))

	gate := planusage.Gate{}
	calls := 0
	s := newTaskScheduler()
	s.budgetGate = func(task.Task) planusage.Gate {
		calls++
		return gate
	}

	assert.False(t, s.skipClosedBudgetGate("plain"))
	assert.Equal(t, 0, calls, "a task without the gate must not read the budget")

	assert.False(t, s.skipClosedBudgetGate("gated"), "an open gate lets the task run")

	gate = planusage.Gate{Name: planusage.GateLimit, Reason: "Plan at 60% of the week (limit 50%) · resumes Mon 12:00"}
	assert.True(t, s.skipClosedBudgetGate("gated"))
	got, err := task.GetTask("gated")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(got.LastRunStatus, TaskStatusSkippedBudgetGate), got.LastRunStatus)
	assert.Contains(t, got.LastRunStatus, "Plan at 60% of the week")
	assert.NotNil(t, got.LastRunAt)

	off := false
	_, err = task.UpdateTask("gated", task.TaskUpdate{BudgetGated: &off}, task.ProjectExpectation{})
	require.NoError(t, err)
	gate = planusage.Gate{Name: planusage.GateLimit, Reason: "closed"}
	assert.False(t, s.skipClosedBudgetGate("gated"), "a task whose gate was turned off runs")
}

func TestActiveLimit(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	obs := func(agent, account string, reset time.Time) session.AccountLimitObservationData {
		return session.AccountLimitObservationData{Agent: agent, Account: account, ResetAt: reset}
	}
	cases := []struct {
		name    string
		obs     []session.AccountLimitObservationData
		account string
		limited bool
		until   time.Time
	}{
		{"none", nil, "default", false, time.Time{}},
		{"empty account is default", []session.AccountLimitObservationData{obs("claude", "", now.Add(time.Hour))}, "default", true, now.Add(time.Hour)},
		{"other account", []session.AccountLimitObservationData{obs("claude", "work", now.Add(time.Hour))}, "default", false, time.Time{}},
		{"other agent", []session.AccountLimitObservationData{obs("codex", "", now.Add(time.Hour))}, "default", false, time.Time{}},
		{"unknown reset", []session.AccountLimitObservationData{obs("claude", "", time.Time{})}, "default", true, time.Time{}},
		{"within the resume grace", []session.AccountLimitObservationData{obs("claude", "", now.Add(-time.Minute))}, "default", true, now.Add(-time.Minute)},
		{"past the resume grace", []session.AccountLimitObservationData{obs("claude", "", now.Add(-limitResumeGrace))}, "default", false, time.Time{}},
		{"latest reset wins", []session.AccountLimitObservationData{
			obs("claude", "work", now.Add(time.Hour)), obs("claude", "work", now.Add(3*time.Hour)),
		}, "work", true, now.Add(3 * time.Hour)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			limited, until := activeLimit(c.obs, "claude", c.account, now)
			assert.Equal(t, c.limited, limited)
			assert.True(t, c.until.Equal(until), "until %v, want %v", until, c.until)
		})
	}
}
