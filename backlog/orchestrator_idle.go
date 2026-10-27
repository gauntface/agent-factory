package backlog

import (
	"maps"
	"strings"
	"time"
)

// ArchiveOrchIdle: an orchestrator's turn ended with no events to deliver.
const ArchiveOrchIdle = "orchestrator_idle"

// OrchTurnEnd is what the end of an orchestrator's turn did to its group.
type OrchTurnEnd struct {
	Group int
	// Changed is whether the group was written: a --stay was used up.
	Changed bool
	// Archive asks the caller to archive the session for ArchiveOrchIdle.
	Archive bool
}

// OrchTurnEnded applies the end of a turn of sessionID. A session that asked
// to stay keeps live for this turn and the request is used up. A session with
// events still to deliver keeps live: the next delivery reaches it. Otherwise
// it asks for an archive. A session that orchestrates no group gets a zero
// OrchTurnEnd.
func (b *Backlog) OrchTurnEnded(sessionID string, now time.Time) OrchTurnEnd {
	id := b.OrchestratedBy(strings.TrimSpace(sessionID))
	if sessionID == "" || id == 0 {
		return OrchTurnEnd{}
	}
	it := b.Item(id)
	o := it.Orchestrator
	if o.Stay {
		o.Stay = false
		it.UpdatedAt = now
		return OrchTurnEnd{Group: id, Changed: true}
	}
	return OrchTurnEnd{Group: id, Archive: b.orchIdle(sessionID)}
}

// orchIdle is whether sessionID orchestrates a group with nothing to send
// it and no request to stay.
func (b *Backlog) orchIdle(sessionID string) bool {
	id := b.OrchestratedBy(sessionID)
	if sessionID == "" || id == 0 {
		return false
	}
	o := b.Item(id).Orchestrator
	return !o.Stay && len(o.Events) == 0 && o.Sending == 0
}

// SetOrchestratorStay asks af not to archive sessionID when its current turn
// ends, and returns the group it orchestrates.
func (b *Backlog) SetOrchestratorStay(sessionID string, now time.Time) (int, error) {
	sessionID = strings.TrimSpace(sessionID)
	id := b.OrchestratedBy(sessionID)
	if sessionID == "" || id == 0 {
		return 0, errNotOrchestrator(sessionID)
	}
	it := b.Item(id)
	if !it.Orchestrator.Stay {
		it.Orchestrator.Stay = true
		it.UpdatedAt = now
	}
	return id, nil
}

// Cost sums the orchestrator's metered transcript costs.
func (o *Orchestrator) Cost() float64 { return RunRef{Meter: o.Meter}.Cost() }

// SetOrchestratorMeters stores per-file meters on the orchestrators of the
// given sessions. It returns whether anything changed.
func (b *Backlog) SetOrchestratorMeters(meters map[string]map[string]Meter) bool {
	changed := false
	for i := range b.Items {
		o := b.Items[i].Orchestrator
		if o == nil || o.SessionID == "" {
			continue
		}
		if m, ok := meters[o.SessionID]; ok && !maps.Equal(o.Meter, m) {
			o.Meter = maps.Clone(m)
			changed = true
		}
	}
	return changed
}
