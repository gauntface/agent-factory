package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/sachiniyer/agent-factory/backlog"
	"github.com/sachiniyer/agent-factory/session"
)

func askInItem(t *testing.T, m *Manager, repoID string, inst *session.Instance, then string) {
	t.Helper()
	if _, err := m.backlogService().PostMessage(backlog.PostMessageRequest{RepoID: repoID, ID: 1,
		Kind: backlog.KindQuestion, Text: "which db?", SessionID: inst.ID, Then: then}); err != nil {
		t.Fatal(err)
	}
}

func TestAskThenArchiveArchivesAtRunEnd(t *testing.T) {
	m, repoID, inst := startedItem(t)
	archived := stubBacklogArchive(t)
	askInItem(t, m, repoID, inst, backlog.ThenArchive)
	idleItemSession(t, inst)
	m.backlogRunEnded(repoID, inst.ID)
	if len(*archived) != 1 || (*archived)[0] != inst.ID {
		t.Fatalf("archived %v", *archived)
	}
	if inst.OwedArchive() != nil {
		t.Fatal("the owed archive outlived the archive")
	}
	if it := loadItem(t, repoID, 1); it.Status != backlog.StatusRunning || it.OpenQuestion() == nil {
		t.Fatalf("item %+v", it)
	}
}

func TestAskThenWaitKeepsSession(t *testing.T) {
	for _, then := range []string{"", backlog.ThenWait} {
		m, repoID, inst := startedItem(t)
		archived := stubBacklogArchive(t)
		askInItem(t, m, repoID, inst, then)
		idleItemSession(t, inst)
		m.backlogRunEnded(repoID, inst.ID)
		if len(*archived) != 0 || inst.OwedArchive() != nil {
			t.Fatalf("then %q archived %v", then, *archived)
		}
	}
}

func TestAskArchiveRefusedKeepsSessionLive(t *testing.T) {
	m, repoID, inst := startedItem(t)
	archived := stubBacklogArchive(t)
	askInItem(t, m, repoID, inst, backlog.ThenArchive)
	idleItemSession(t, inst)
	if err := inst.NoteAdoptionDelivery(); err != nil {
		t.Fatal(err)
	}
	m.backlogRunEnded(repoID, inst.ID)
	if len(*archived) != 0 || inst.OwedArchive() != nil {
		t.Fatalf("archived %v, owed %+v", *archived, inst.OwedArchive())
	}
	it := loadItem(t, repoID, 1)
	last := it.Thread[len(it.Thread)-1]
	if last.Author != backlog.AuthorAF || !strings.HasPrefix(last.Text, "Kept the session live: ") {
		t.Fatalf("note %+v", last)
	}
	if it.OpenQuestion().Then != backlog.ThenWait {
		t.Fatal("a kept session would be archived again")
	}
}

func TestAnswerCancelsOwedArchive(t *testing.T) {
	m, repoID, inst := startedItem(t)
	archived := stubBacklogArchive(t)
	askInItem(t, m, repoID, inst, backlog.ThenArchive)
	idleItemSession(t, inst)
	inst.SetOwedArchive(&session.OwedArchiveData{Reason: backlog.ArchiveAsk, FiledAt: time.Now()})
	if _, err := backlog.Update(repoID, func(b *backlog.Backlog) error {
		_, err := b.Answer(1, 0, "postgres", false, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	m.archiveItemSession(repoID, inst.ID, backlog.ArchiveAsk)
	if len(*archived) != 0 || inst.OwedArchive() != nil {
		t.Fatalf("archived %v, owed %+v", *archived, inst.OwedArchive())
	}
}

func TestOwedArchiveRedrivenAfterRestart(t *testing.T) {
	m, repoID, inst := startedItem(t)
	archived := stubBacklogArchive(t)
	askInItem(t, m, repoID, inst, backlog.ThenArchive)
	idleItemSession(t, inst)
	inst.SetOwedArchive(&session.OwedArchiveData{Reason: backlog.ArchiveAsk, FiledAt: time.Now()})
	m.redriveOwedArchives(repoID, m.itemSessions(repoID), map[string]bool{})
	if len(*archived) != 1 {
		t.Fatalf("archived %v", *archived)
	}
}

func TestBacklogArchiveGuardUsesFiledAt(t *testing.T) {
	_, _, inst := startedItem(t)
	idleItemSession(t, inst)
	guard := backlogArchiveGuard(inst.ID)
	inst.SetOwedArchive(&session.OwedArchiveData{Reason: backlog.ArchiveAsk, FiledAt: time.Now().Add(time.Hour)})
	if err := guard(inst); err != nil {
		t.Fatalf("refused: %v", err)
	}
	inst.SetOwedArchive(&session.OwedArchiveData{Reason: backlog.ArchiveAsk, FiledAt: inst.BacklogRunEndedAt().Add(-time.Minute)})
	if err := guard(inst); err == nil || !strings.Contains(err.Error(), "run ended after") {
		t.Fatalf("a run end after the filing passed: %v", err)
	}
}

func TestNewRunDropsOwedArchive(t *testing.T) {
	_, _, inst := startedItem(t)
	idleItemSession(t, inst)
	inst.SetOwedArchive(&session.OwedArchiveData{Reason: backlog.ArchiveAsk, FiledAt: time.Now()})
	inst.ArmBacklogRun(time.Now())
	if inst.OwedArchive() != nil {
		t.Fatal("a new run kept the owed archive")
	}
}
