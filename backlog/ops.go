package backlog

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// Delete removes a draft that has no runs, no thread and no children. Links
// that wait on it are removed too. It returns id and the ids whose waits-on
// changed. On error b is unchanged.
func (b *Backlog) Delete(id int) ([]int, error) {
	it, err := b.get(id)
	if err != nil {
		return nil, err
	}
	if b.IsParent(id) {
		return nil, fmt.Errorf("%s has children; delete or move them first, or drop it instead", FormatID(id))
	}
	if it.Status != StatusDraft {
		return nil, fmt.Errorf("%s is %s; only a draft can be deleted — drop it instead", FormatID(id), it.Status)
	}
	if len(it.Runs) > 0 || len(it.Thread) > 0 {
		return nil, fmt.Errorf("%s is a draft with runs or a thread; drop it instead", FormatID(id))
	}
	i := b.index(id)
	b.Items = append(b.Items[:i], b.Items[i+1:]...)
	changed := []int{id}
	for j := range b.Items {
		other := &b.Items[j]
		if slices.Contains(other.WaitsOn, id) {
			other.WaitsOn = slices.DeleteFunc(other.WaitsOn, func(x int) bool { return x == id })
			changed = append(changed, other.ID)
		}
	}
	return changed, nil
}

// AddNote appends a note to the item's thread. A note is never delivered to a
// session.
func (b *Backlog) AddNote(id int, author, text string, now time.Time) (*Message, error) {
	it, err := b.get(id)
	if err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("a note needs text")
	}
	next := 1
	for _, m := range it.Thread {
		if m.ID >= next {
			next = m.ID + 1
		}
	}
	it.Thread = append(it.Thread, Message{ID: next, Kind: KindNote, Author: author, Text: text, At: now})
	it.UpdatedAt = now
	return &it.Thread[len(it.Thread)-1], nil
}

// SettingsChange sets the fields that are not nil.
type SettingsChange struct {
	Runner     *bool
	MaxRunning *int
	OnDone     *string
}

// ApplySettings validates and applies c. On error b is unchanged.
func (b *Backlog) ApplySettings(c SettingsChange) error {
	s := b.Settings
	if c.Runner != nil {
		s.Runner = *c.Runner
	}
	if c.MaxRunning != nil {
		if *c.MaxRunning < 1 || *c.MaxRunning > MaxMaxRunning {
			return fmt.Errorf("max running must be from 1 to %d", MaxMaxRunning)
		}
		s.MaxRunning = *c.MaxRunning
	}
	if c.OnDone != nil {
		if *c.OnDone != OnDoneKeep && *c.OnDone != OnDoneArchive {
			return fmt.Errorf("on done must be %s or %s", OnDoneKeep, OnDoneArchive)
		}
		s.OnDone = *c.OnDone
	}
	b.Settings = s
	return nil
}

// Apply validates and applies one change to the global budget. At most one of
// Add, Set and Reset may be given; MaxPerRun may go with any of them. On error
// b is unchanged.
func (c SetBudgetRequest) Apply(b *Budget, now time.Time) error {
	for _, v := range []*float64{c.Add, c.Set, c.MaxPerRun} {
		if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0)) {
			return errors.New("an amount must be a finite number of US dollars")
		}
	}
	n := 0
	for _, set := range []bool{c.Add != nil, c.Set != nil, c.Reset} {
		if set {
			n++
		}
	}
	if n > 1 {
		return errors.New("use one of add, set and reset")
	}
	if n == 0 && c.MaxPerRun == nil {
		return errors.New("nothing to change — use add, set, reset or max per run")
	}
	next := *b
	switch {
	case c.Add != nil:
		if *c.Add <= 0 {
			return errors.New("add needs an amount above $0")
		}
		next.Limit += *c.Add
		if math.IsInf(next.Limit, 0) {
			return errors.New("the budget is too large")
		}
	case c.Set != nil:
		if *c.Set < 0 {
			return errors.New("the budget cannot be below $0")
		}
		next.Limit = *c.Set
	case c.Reset:
		next.Spent = 0
		next.ResetAt = now
	}
	if c.MaxPerRun != nil {
		if *c.MaxPerRun < 0 {
			return errors.New("the cap per run cannot be below $0 — use 0 for no cap")
		}
		next.MaxPerRun = *c.MaxPerRun
	}
	*b = next
	return nil
}

// Cost is the metered cost of the run, in US dollars.
func (r RunRef) Cost() float64 {
	total := 0.0
	for _, m := range r.Meter {
		total += m.Cost
	}
	return total
}

// NeededBy returns the ids of the items that wait on id, in tree order.
func (b *Backlog) NeededBy(id int) []int {
	var out []int
	for _, it := range b.Items {
		for _, w := range it.WaitsOn {
			if w == id {
				out = append(out, it.ID)
				break
			}
		}
	}
	return out
}
