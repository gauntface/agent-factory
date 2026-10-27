package backlog

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// What a question's session does after asking.
const (
	ThenWait    = "wait"
	ThenArchive = "archive"
)

// MaxHandoffBytes caps a question's handoff.
const MaxHandoffBytes = 8 << 10

// Why af archives an item session.
const (
	// ArchiveDone: the user marked the item done and on_done is archive.
	ArchiveDone = "done"
	// ArchiveAsk: the run ended with an open question asked with then archive.
	ArchiveAsk = "ask"
)

// Question is what an agent asks. Then is "", ThenWait or ThenArchive; ""
// waits, as a question from before then existed does.
type Question struct {
	Text    string
	Then    string
	Handoff string
}

func (q Question) check() error {
	switch q.Then {
	case "", ThenWait, ThenArchive:
	default:
		return fmt.Errorf("then %q is not one of %s and %s", q.Then, ThenWait, ThenArchive)
	}
	if n := len(strings.TrimSpace(q.Handoff)); n > MaxHandoffBytes {
		return fmt.Errorf("the handoff is %d bytes; the limit is 8 KB (%d bytes)", n, MaxHandoffBytes)
	}
	return nil
}

func (it *Item) openQuestions() []*Message {
	var out []*Message
	for i := range it.Thread {
		if m := &it.Thread[i]; m.Kind == KindQuestion && it.questionOpen(m.ID) {
			out = append(out, m)
		}
	}
	return out
}

// archiveQuestion is the item's open question asked with then archive, or nil.
func (it *Item) archiveQuestion() *Message {
	for _, m := range it.openQuestions() {
		if m.Then == ThenArchive {
			return m
		}
	}
	return nil
}

// ArchiveOwed reports whether af still owes an archive of sessionID for
// reason: the session is its item's latest run and the reason still holds.
func (b *Backlog) ArchiveOwed(sessionID, reason string) bool {
	it := b.ItemForSession(sessionID)
	if it == nil || it.lastRun().SessionID != sessionID {
		return false
	}
	switch reason {
	case ArchiveDone:
		return it.Status == StatusDone && b.Settings.OnDone == OnDoneArchive
	case ArchiveAsk:
		return it.Status.Open() && it.archiveQuestion() != nil
	}
	return false
}

// KeptLiveNote is the note af adds when it does not archive a session.
func KeptLiveNote(reason string) string { return "Kept the session live: " + reason }

// KeptLive records that af did not archive sessionID, owed for reason,
// because of why: a note, and the open question's then becomes wait, so a
// later run end or restart does not try again. It returns the item id, or 0
// when the archive is no longer owed, as after an answer, and nothing changed.
func (b *Backlog) KeptLive(sessionID, reason, why string, now time.Time) (int, error) {
	if !b.ArchiveOwed(sessionID, reason) {
		return 0, nil
	}
	it := b.ItemForSession(sessionID)
	if q := it.archiveQuestion(); q != nil {
		q.Then = ThenWait
	}
	if _, err := b.AddNote(it.ID, AuthorAF, KeptLiveNote(why), now); err != nil {
		return 0, err
	}
	return it.ID, nil
}

// InboxEntry is one open question in the project's inbox.
type InboxEntry struct {
	ID       int     `json:"id"`
	Title    string  `json:"title"`
	Question Message `json:"question"`
}

// Inbox lists every open question of every item that is with the user,
// oldest first. Questions waiting for a group's orchestrator and items in an
// archived group are left out.
func (b *Backlog) Inbox() []InboxEntry {
	out := []InboxEntry{}
	for i := range b.Items {
		it := &b.Items[i]
		if b.ArchivedBy(it.ID) != 0 {
			continue
		}
		for _, q := range it.openQuestions() {
			if !b.withOrchestrator(it.ID, q) {
				out = append(out, InboxEntry{ID: it.ID, Title: it.Title, Question: *q})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Question.At.Before(out[j].Question.At) })
	return out
}

// InboxNote is one note the Inbox shows; it needs no action.
type InboxNote struct {
	ID    int     `json:"id"`
	Title string  `json:"title"`
	Note  Message `json:"note"`
}

// InboxNotes lists the Inbox notes of every item, oldest first. Items in an
// archived group are left out.
func (b *Backlog) InboxNotes() []InboxNote {
	out := []InboxNote{}
	for i := range b.Items {
		it := &b.Items[i]
		if b.ArchivedBy(it.ID) != 0 {
			continue
		}
		for _, m := range it.Thread {
			if m.Kind == KindNote && m.Inbox {
				out = append(out, InboxNote{ID: it.ID, Title: it.Title, Note: m})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Note.At.Before(out[j].Note.At) })
	return out
}
