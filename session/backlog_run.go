package session

import "time"

// runGateActiveLocked reports whether a task run or a backlog run is in flight.
// Both use the same completion gate. Caller holds i.mu.
func (i *Instance) runGateActiveLocked() bool {
	return i.taskRunActive || i.backlogRunActive
}

func runGateActiveInData(data InstanceData) bool {
	return data.TaskRunActive || (data.BacklogRunActive && data.BacklogItem != 0)
}

// endRunsLocked ends the run in flight on its own edge. Caller holds i.mu.
func (i *Instance) endRunsLocked() {
	task := i.taskRunActive
	i.taskRunActive = false
	i.backlogRunActive = false
	i.taskRunIdleEdgeHeld = false
	i.taskRunTurnGateHeld = false
	i.touchLocked()
	if task {
		i.captureAdoptionBaselineLocked()
	}
}

// BacklogRunActive reports whether the backlog run on this item session is in
// flight: armed by a backlog delivery and not yet ended by an idle edge or a
// teardown.
func (i *Instance) BacklogRunActive() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.backlogRunActive
}

// ArmBacklogRun opens a backlog run at the moment the backlog delivers a prompt.
// It sets the run's prompt boundary and holds the turn gate, so the session's
// next idle observation ends the run only after the agent has taken the turn.
// It reports false for a session that is not an item session.
func (i *Instance) ArmBacklogRun(at time.Time) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.BacklogItem == 0 || i.taskRunActive {
		return false
	}
	i.backlogRunActive = true
	i.taskRunPromptAttemptAt = at
	i.taskRunTurnObservedAt = time.Time{}
	i.taskRunIdleEdgeHeld = false
	i.taskRunTurnGateHeld = true
	i.stateEpoch++
	i.touchLocked()
	return true
}

// DisarmBacklogRun closes a backlog run whose prompt was not delivered.
func (i *Instance) DisarmBacklogRun() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.backlogRunActive {
		return false
	}
	i.backlogRunActive = false
	i.taskRunPromptAttemptAt = time.Time{}
	i.taskRunTurnObservedAt = time.Time{}
	i.taskRunIdleEdgeHeld = false
	i.taskRunTurnGateHeld = false
	i.touchLocked()
	return true
}

// ReconcileBacklogRunState mirrors the daemon's backlog run marker onto a client
// row, like ReconcileTaskRunState.
func (i *Instance) ReconcileBacklogRunState(active bool) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.backlogRunActive == active {
		return false
	}
	i.backlogRunActive = active
	i.touchLocked()
	return true
}

// RunGateActive reports whether a task run or a backlog run is in flight, so
// turn evidence is worth recording.
func (i *Instance) RunGateActive() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.runGateActiveLocked()
}
