// Package backlog holds the ordered tree of work items for one project and the
// global budget, the Claude plan use under which the runner starts items.
package backlog

import (
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"
)

type Status string

const (
	StatusDraft   Status = "draft"
	StatusQueued  Status = "queued"
	StatusRunning Status = "running"
	StatusReview  Status = "review"
	StatusDone    Status = "done"
	StatusDropped Status = "dropped"
)

func (s Status) Valid() bool {
	switch s {
	case StatusDraft, StatusQueued, StatusRunning, StatusReview, StatusDone, StatusDropped:
		return true
	}
	return false
}

func (s Status) Open() bool { return s != StatusDone && s != StatusDropped }

const (
	RunStarting = "starting"
	RunStarted  = "started"
	RunFailed   = "failed"

	StartedByRunner = "runner"
	StartedByUser   = "user"

	KindQuestion = "question"
	KindAnswer   = "answer"
	KindNote     = "note"

	AuthorUser = "user"
	AuthorAF   = "af"

	ClosedAnswered          = "answered"
	ClosedAnsweredInSession = "answered in session"
	ClosedSentToReview      = "sent to review"
	ClosedDone              = "done"
	ClosedDropped           = "dropped"
	// ClosedRebound and ClosedUnbound close af's escalation question on a
	// group when its orchestrator changes or is cleared.
	ClosedRebound = "orchestrator rebound"
	ClosedUnbound = "orchestrator unbound"

	DeliveryPending   = "pending"
	DeliveryDelivered = "delivered"
	DeliveryFailed    = "failed"
	// DeliveryNewRun is an answer waiting to start a new run because the
	// session that asked is gone; DeliverySuperseded is one a later answer to
	// the same question replaced. Neither closes its question.
	DeliveryNewRun     = "new_run"
	DeliverySuperseded = "superseded"

	OnDoneKeep    = "keep"
	OnDoneArchive = "archive"

	DefaultMaxRunning = 1
	MaxMaxRunning     = 8
)

type Backlog struct {
	SchemaVersion int      `json:"schema_version"`
	NextID        int      `json:"next_id"`
	Settings      Settings `json:"settings"`
	Items         []Item   `json:"items"`
}

type Item struct {
	ID         int       `json:"id"`
	Title      string    `json:"title"`
	Body       string    `json:"body,omitempty"`
	Status     Status    `json:"status"`
	Parent     int       `json:"parent,omitempty"`
	Sequential bool      `json:"sequential,omitempty"`
	WaitsOn    []int     `json:"waits_on,omitempty"`
	Program    string    `json:"program,omitempty"`
	Source     *Source   `json:"source,omitempty"`
	PR         string    `json:"pr,omitempty"`
	External   string    `json:"external,omitempty"`
	Runs       []RunRef  `json:"runs,omitempty"`
	Thread     []Message `json:"thread,omitempty"`
	Paused     bool      `json:"paused,omitempty"`
	Archived   bool      `json:"archived,omitempty"`
	// Orchestrator is set only on a top-level parent.
	Orchestrator *Orchestrator `json:"orchestrator,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

type Source struct {
	Proposal string `json:"proposal"`
	Key      string `json:"key"`
}

type RunRef struct {
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	State     string `json:"state"`
	Branch    string `json:"branch,omitempty"`
	// Answer is the kept answer this run's first prompt carries.
	Answer     int              `json:"answer,omitempty"`
	PrevStatus Status           `json:"prev_status"`
	StartedBy  string           `json:"started_by"`
	StartedAt  time.Time        `json:"started_at"`
	Meter      map[string]Meter `json:"meter,omitempty"`
	// EndedAt is when a failed start failed.
	EndedAt time.Time `json:"ended_at,omitzero"`
	// CapReset names the 5-hour window the cap per run counts in, by its reset
	// in epoch seconds; CapBase is the run's cost the cap counts from: at the
	// window's start, or when af last stopped the run at the cap.
	CapReset int64   `json:"cap_reset,omitempty"`
	CapBase  float64 `json:"cap_base,omitempty"`
	// StalledAt is when af last told the group's orchestrator this run
	// stalled; only work after it starts a new idle period.
	StalledAt time.Time `json:"stalled_at,omitzero"`
}

type Meter struct {
	Offset int64   `json:"offset"`
	Cost   float64 `json:"cost"`
	// Last is the last message id counted, so a message whose lines straddle
	// two reads is counted once.
	Last string `json:"last,omitempty"`
}

type Message struct {
	ID      int       `json:"id"`
	Kind    string    `json:"kind"`
	Author  string    `json:"author"`
	Text    string    `json:"text"`
	Answers int       `json:"answers,omitempty"`
	Closed  string    `json:"closed,omitempty"`
	At      time.Time `json:"at"`
	// Delivery tracks an answer on its way to the item's session, so a daemon
	// restart can finish it. PrevStatus is the item status the answer replaced,
	// put back if the delivery fails.
	Delivery   string `json:"delivery,omitempty"`
	PrevStatus Status `json:"prev_status,omitempty"`
	// Then is what a question's session does after asking: "wait" or
	// "archive". Handoff is the asking agent's notes for a later run.
	Then    string `json:"then,omitempty"`
	Handoff string `json:"handoff,omitempty"`
	// Route is where a question goes: RouteOrchestrator, RouteUser, or ""
	// for the user.
	Route string `json:"route,omitempty"`
	// Inbox marks a note the Inbox shows, such as the one af adds when an
	// orchestrator marks an item done.
	Inbox bool `json:"inbox,omitempty"`
}

type Settings struct {
	Runner     bool   `json:"runner"`
	MaxRunning int    `json:"max_running"`
	OnDone     string `json:"on_done"`
}

func DefaultSettings() Settings {
	return Settings{MaxRunning: DefaultMaxRunning, OnDone: OnDoneKeep}
}

// New returns an empty backlog at the current schema version.
func New() *Backlog {
	return &Backlog{SchemaVersion: BacklogSchemaVersion, NextID: 1, Settings: DefaultSettings(), Items: []Item{}}
}

// FormatID renders an item id the way every surface shows it: B-12.
func FormatID(id int) string { return "B-" + strconv.Itoa(id) }

// ParseID accepts "12" or "B-12" (any case for the prefix).
func ParseID(s string) (int, error) {
	t := strings.TrimSpace(s)
	if len(t) > 2 && (t[0] == 'B' || t[0] == 'b') && t[1] == '-' {
		t = t[2:]
	}
	n, err := strconv.Atoi(t)
	if err != nil || n <= 0 || strings.HasPrefix(t, "+") {
		return 0, fmt.Errorf("invalid item id %q — use a number like 12 or B-12", s)
	}
	return n, nil
}

// NotFoundError is returned when an item id is not in the backlog. Project is
// optional; callers that know the project name set it.
type NotFoundError struct {
	ID      int
	Project string
}

func (e *NotFoundError) Error() string {
	if e.Project != "" {
		return fmt.Sprintf("%s not found in project %s — run \"af backlog list --all\"", FormatID(e.ID), e.Project)
	}
	return fmt.Sprintf("%s not found — run \"af backlog list --all\"", FormatID(e.ID))
}

// Item returns the item with id, or nil.
func (b *Backlog) Item(id int) *Item {
	if i := b.index(id); i >= 0 {
		return &b.Items[i]
	}
	return nil
}

func (b *Backlog) index(id int) int {
	for i := range b.Items {
		if b.Items[i].ID == id {
			return i
		}
	}
	return -1
}

func (b *Backlog) get(id int) (*Item, error) {
	if it := b.Item(id); it != nil {
		return it, nil
	}
	return nil, &NotFoundError{ID: id}
}

// OpenQuestion returns the item's oldest question that no answer names and
// that is not closed, or nil.
func (it *Item) OpenQuestion() *Message {
	answered := map[int]bool{}
	for _, m := range it.Thread {
		if m.closesQuestion() {
			answered[m.Answers] = true
		}
	}
	for i := range it.Thread {
		m := &it.Thread[i]
		if m.Kind == KindQuestion && m.Closed == "" && !answered[m.ID] {
			return m
		}
	}
	return nil
}

func (it *Item) closeOpenQuestions(reason string) {
	for q := it.OpenQuestion(); q != nil; q = it.OpenQuestion() {
		q.Closed = reason
	}
}

// closeAFQuestions closes the open questions af asked, such as the cap per
// run question; the agent's own questions stay open.
func (it *Item) closeAFQuestions(reason string) {
	for i := range it.Thread {
		if m := &it.Thread[i]; m.Kind == KindQuestion && m.Author == AuthorAF && it.questionOpen(m.ID) {
			m.Closed = reason
		}
	}
}

// closesQuestion is whether m is an answer that closes the question it names.
func (m Message) closesQuestion() bool {
	return m.Kind == KindAnswer && m.Answers != 0 && m.Delivery != DeliveryNewRun && m.Delivery != DeliverySuperseded
}

// KeptAnswer is the latest answer waiting for a new run whose question is
// still open, or nil.
func (it *Item) KeptAnswer() *Message {
	for i := len(it.Thread) - 1; i >= 0; i-- {
		if m := &it.Thread[i]; m.Kind == KindAnswer && m.Delivery == DeliveryNewRun && it.questionOpen(m.Answers) {
			return m
		}
	}
	return nil
}

// answerStarting is whether the item's latest run is starting with a kept
// answer.
func (it *Item) answerStarting() bool {
	r := it.lastRun()
	return r != nil && r.State == RunStarting && r.Answer != 0
}

func (it *Item) lastRun() *RunRef {
	if len(it.Runs) == 0 {
		return nil
	}
	return &it.Runs[len(it.Runs)-1]
}

func (b *Backlog) clone() *Backlog {
	c := *b
	c.Items = make([]Item, len(b.Items))
	for i, it := range b.Items {
		it.WaitsOn = append([]int(nil), it.WaitsOn...)
		it.Runs = append([]RunRef(nil), it.Runs...)
		for r := range it.Runs {
			it.Runs[r].Meter = maps.Clone(it.Runs[r].Meter)
		}
		it.Thread = append([]Message(nil), it.Thread...)
		if it.Source != nil {
			s := *it.Source
			it.Source = &s
		}
		it.Orchestrator = it.Orchestrator.clone()
		c.Items[i] = it
	}
	return &c
}
