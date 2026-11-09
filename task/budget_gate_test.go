package task

import (
	"bytes"
	"encoding/gob"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateTriggerBudgetGated(t *testing.T) {
	cases := []struct {
		name    string
		task    Task
		wantErr string
	}{
		{name: "cron task", task: Task{ID: "t1", CronExpr: "0 3 * * *", Prompt: "p", Enabled: true, BudgetGated: true}},
		{name: "cron task with claude", task: Task{ID: "t2", CronExpr: "0 3 * * *", Prompt: "p", Program: "claude", Enabled: true, BudgetGated: true}},
		{name: "watch task", task: Task{ID: "t3", WatchCmd: "tail -f log", Enabled: true, BudgetGated: true}, wantErr: "watch task"},
		{name: "other program", task: Task{ID: "t4", CronExpr: "0 3 * * *", Prompt: "p", Program: "codex", Enabled: true, BudgetGated: true}, wantErr: "Claude plan use only"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.task.ValidateTrigger()
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			if assert.Error(t, err) {
				assert.Contains(t, err.Error(), tc.wantErr)
			}
		})
	}
}

func TestTaskUpdateBudgetGated(t *testing.T) {
	off := false
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(TaskUpdate{BudgetGated: &off}))
	var back TaskUpdate
	require.NoError(t, gob.NewDecoder(&buf).Decode(&back))
	require.NotNil(t, back.BudgetGated)
	assert.False(t, *back.BudgetGated)
	assert.False(t, back.IsEmpty())

	base := Task{ID: "t1", CronExpr: "0 3 * * *", Prompt: "p", BudgetGated: true}
	assert.True(t, TaskUpdate{}.apply(base).BudgetGated)
	assert.False(t, TaskUpdate{BudgetGated: &off}.apply(base).BudgetGated)

	diff := DiffTask(base, Task{ID: "t1", CronExpr: "0 3 * * *", Prompt: "p"})
	require.NotNil(t, diff.BudgetGated)
	assert.False(t, *diff.BudgetGated)
}

func TestUpdateTaskBudgetGated(t *testing.T) {
	t.Setenv("AGENT_FACTORY_HOME", t.TempDir())
	require.NoError(t, AddTask(Task{ID: "bg1", Name: "b", CronExpr: "0 9 * * *", Prompt: "p", Enabled: true, BudgetGated: true}))

	got, err := GetTask("bg1")
	require.NoError(t, err)
	assert.True(t, got.BudgetGated)

	watch, empty := "tail -f x", ""
	merged, err := UpdateTask("bg1", TaskUpdate{WatchCmd: &watch, CronExpr: &empty}, ProjectExpectation{})
	require.NoError(t, err, "a move to watch must clear a gate the caller did not patch")
	assert.False(t, merged.BudgetGated)
	assert.Contains(t, merged.Audit[len(merged.Audit)-1].Fields, "budget_gated")

	on := true
	_, err = UpdateTask("bg1", TaskUpdate{BudgetGated: &on}, ProjectExpectation{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "watch task")
}
