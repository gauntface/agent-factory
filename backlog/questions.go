package backlog

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// AnswerPrompt is what af delivers to the item's session with an answer.
func AnswerPrompt(it *Item, text string) string {
	return fmt.Sprintf("The user answered your question on %s: %s", FormatID(it.ID), text)
}

// GoneError is returned when an answer cannot reach the item's session. The
// answer is still written to the thread.
func GoneError(id int) error {
	return fmt.Errorf("the session for %s is gone; start it again with \"af backlog start %d\" — your answer is kept in the thread", FormatID(id), id)
}

func noOpenQuestion(id int) error {
	return fmt.Errorf("%s has no open question — use \"af backlog note\" to add a note", FormatID(id))
}

func (it *Item) nextMessageID() int {
	next := 1
	for _, m := range it.Thread {
		if m.ID >= next {
			next = m.ID + 1
		}
	}
	return next
}

func (it *Item) appendMessage(m Message, now time.Time) *Message {
	m.ID = it.nextMessageID()
	m.At = now
	it.Thread = append(it.Thread, m)
	it.UpdatedAt = now
	return &it.Thread[len(it.Thread)-1]
}

// Ask appends a question from the agent of sessionID, which must be the item's
// latest run. The item must be open.
func (b *Backlog) Ask(id int, sessionID, text string, now time.Time) (*Message, error) {
	it, err := b.get(id)
	if err != nil {
		return nil, err
	}
	if last := it.lastRun(); last == nil || last.SessionID == "" || last.SessionID != sessionID {
		return nil, fmt.Errorf("session %s is not the current run of %s; only that session can ask on it", sessionID, FormatID(id))
	}
	if !it.Status.Open() {
		return nil, fmt.Errorf("%s is %s; questions go on open items", FormatID(id), it.Status)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("a question needs text")
	}
	return it.appendMessage(Message{Kind: KindQuestion, Author: "agent:" + sessionID, Text: text}, now), nil
}

// Answered is what Answer did.
type Answered struct {
	Answer Message
	// SessionID is the item's latest run, the session the answer goes to; ""
	// when the item never started.
	SessionID string
	// Next is the oldest question still open after the answer, or nil.
	Next *Message
}

// Answer appends the user's answer to question questionID, or to the oldest
// open question when questionID is 0, and closes it. With resume the item is
// set running, since af delivers the answer to its session. On error b is
// unchanged.
func (b *Backlog) Answer(id, questionID int, text string, resume bool, now time.Time) (Answered, error) {
	it, err := b.get(id)
	if err != nil {
		return Answered{}, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Answered{}, errors.New("an answer needs text")
	}
	q := it.OpenQuestion()
	if questionID != 0 {
		q = nil
		for i := range it.Thread {
			if m := &it.Thread[i]; m.ID == questionID && m.Kind == KindQuestion {
				q = m
			}
		}
		if q == nil || !it.questionOpen(q.ID) {
			return Answered{}, fmt.Errorf("%s has no open question %d — use \"af backlog note\" to add a note", FormatID(id), questionID)
		}
	}
	if q == nil {
		return Answered{}, noOpenQuestion(id)
	}
	q.Closed = ClosedAnswered
	out := Answered{Answer: *it.appendMessage(Message{Kind: KindAnswer, Author: AuthorUser, Text: text, Answers: q.ID}, now)}
	if last := it.lastRun(); last != nil {
		out.SessionID = last.SessionID
	}
	if resume && it.Status.Open() {
		it.Status = StatusRunning
	}
	if n := it.OpenQuestion(); n != nil {
		c := *n
		out.Next = &c
	}
	return out, nil
}

func (it *Item) questionOpen(id int) bool {
	for _, m := range it.Thread {
		if m.Kind == KindAnswer && m.Answers == id {
			return false
		}
	}
	for _, m := range it.Thread {
		if m.ID == id {
			return m.Kind == KindQuestion && m.Closed == ""
		}
	}
	return false
}

// AnsweredInSession closes the open questions of the item whose latest run is
// sessionID: the user answered in the session's terminal. It returns the item
// id, or 0 when nothing was open.
func (b *Backlog) AnsweredInSession(sessionID string, now time.Time) int {
	it := b.ItemForSession(sessionID)
	if it == nil || it.lastRun().SessionID != sessionID || it.OpenQuestion() == nil {
		return 0
	}
	it.closeOpenQuestions(ClosedAnsweredInSession)
	it.UpdatedAt = now
	return it.ID
}

// questionSessions maps each item with an open question to its latest run's
// session id.
func (b *Backlog) questionSessions() map[int]string {
	out := map[int]string{}
	for i := range b.Items {
		it := &b.Items[i]
		if it.OpenQuestion() == nil {
			continue
		}
		if last := it.lastRun(); last != nil && last.SessionID != "" {
			out[it.ID] = last.SessionID
		}
	}
	return out
}
