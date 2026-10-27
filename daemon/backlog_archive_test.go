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
	m.archiveBacklogSession(repoID, inst.ID, backlog.ArchiveAsk)
	if len(*archived) != 0 || inst.OwedArchive() != nil {
		t.Fatalf("archived %v, owed %+v", *archived, inst.OwedArchive())
	}
}

// restartInstance reloads inst from its persisted row through
// FromInstanceData, as a daemon restart does, so the in-memory delivery
// counters start at 0, and puts the reloaded instance in m's place.
func restartInstance(t *testing.T, m *Manager, repoID string, inst *session.Instance, edit func(*session.InstanceData)) *session.Instance {
	t.Helper()
	data := persistedInstanceByTitle(t, repoID, inst.Title)
	data.BackendType = "docker"
	if edit != nil {
		edit(&data)
	}
	reloaded, err := session.FromInstanceData(data)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.SetBackend(session.NewFakeBackend())
	if reloaded.GetLiveness() != session.LiveReady {
		if err := reloaded.Transition(session.ObserveLiveness(session.LiveReady)); err != nil {
			t.Fatal(err)
		}
	}
	m.mu.Lock()
	m.instances[daemonInstanceKey(repoID, inst.Title)] = reloaded
	m.mu.Unlock()
	return reloaded
}

// owedAskArchive leaves item 1 asking with then archive on an idle session
// whose owed archive is filed and on disk.
func owedAskArchive(t *testing.T) (*Manager, string, *session.Instance) {
	t.Helper()
	m, repoID, inst := startedItem(t)
	askInItem(t, m, repoID, inst, backlog.ThenArchive)
	idleItemSession(t, inst)
	m.fileOwedArchive(repoID, inst, backlog.ArchiveAsk)
	return m, repoID, inst
}

func TestFileOwedArchivePersists(t *testing.T) {
	_, repoID, inst := owedAskArchive(t)
	d := persistedInstanceByTitle(t, repoID, inst.Title)
	if d.OwedArchive == nil || d.OwedArchive.Reason != backlog.ArchiveAsk || !d.OwedArchive.FiledAt.Equal(inst.OwedArchive().FiledAt) {
		t.Fatalf("persisted %+v", d.OwedArchive)
	}
}

func TestOwedArchiveRedrivenAfterRestart(t *testing.T) {
	m, repoID, inst := owedAskArchive(t)
	archived := stubBacklogArchive(t)
	reloaded := restartInstance(t, m, repoID, inst, nil)
	if reloaded.AdoptionDeliveries() != 0 || reloaded.OwedArchive() == nil {
		t.Fatalf("deliveries %d owed %+v", reloaded.AdoptionDeliveries(), reloaded.OwedArchive())
	}
	m.redriveOwedArchives(repoID, m.itemSessions(repoID), map[string]bool{})
	if len(*archived) != 1 || (*archived)[0] != inst.ID {
		t.Fatalf("archived %v", *archived)
	}
}

func TestOwedArchiveRefusedOnChurnAfterFiledAt(t *testing.T) {
	m, repoID, inst := owedAskArchive(t)
	archived := stubBacklogArchive(t)
	filed := inst.OwedArchive().FiledAt
	restartInstance(t, m, repoID, inst, func(d *session.InstanceData) {
		d.LastPaneChurnAt = filed.Add(time.Second)
	})
	m.redriveOwedArchives(repoID, m.itemSessions(repoID), map[string]bool{})
	if len(*archived) != 0 {
		t.Fatalf("archived %v after pane churn", *archived)
	}
	it := loadItem(t, repoID, 1)
	if !strings.HasPrefix(lastNote(it).Text, "Kept the session live: ") || it.OpenQuestion().Then != backlog.ThenWait {
		t.Fatalf("item %+v", it)
	}
}

func TestOwedArchiveWaitsForIdle(t *testing.T) {
	m, repoID, inst := owedAskArchive(t)
	archived := stubBacklogArchive(t)
	if err := inst.Transition(session.ObserveLiveness(session.LiveLost)); err != nil {
		t.Fatal(err)
	}
	m.redriveOwedArchives(repoID, m.itemSessions(repoID), map[string]bool{})
	it := loadItem(t, repoID, 1)
	if len(*archived) != 0 || inst.OwedArchive() == nil || it.OpenQuestion().Then != backlog.ThenArchive {
		t.Fatalf("archived %v owed %+v item %+v", *archived, inst.OwedArchive(), it)
	}
	m.drainBacklogDeferredArchive(repoID, inst)
	m.backgroundMutationWG.Wait()
	if len(*archived) != 0 {
		t.Fatal("archived while not idle")
	}
	if err := inst.Transition(session.ObserveLiveness(session.LiveReady)); err != nil {
		t.Fatal(err)
	}
	m.drainBacklogDeferredArchive(repoID, inst)
	m.backgroundMutationWG.Wait()
	if len(*archived) != 1 {
		t.Fatalf("archived %v once idle", *archived)
	}
}

func TestAskArchiveDeferredWhileAttached(t *testing.T) {
	m, repoID, inst := startedItem(t)
	archived := stubBacklogArchive(t)
	askInItem(t, m, repoID, inst, backlog.ThenArchive)
	idleItemSession(t, inst)
	m.PauseStatusPoll(repoID, inst.Title, inst.ID)
	m.backlogRunEnded(repoID, inst.ID)
	if len(*archived) != 0 {
		t.Fatal("archived while attached")
	}
	if d := persistedInstanceByTitle(t, repoID, inst.Title); d.OwedArchive == nil || d.OwedArchive.Reason != backlog.ArchiveAsk {
		t.Fatalf("persisted %+v", d.OwedArchive)
	}
	m.ResumeStatusPoll(repoID, inst.Title, inst.ID)
	m.drainBacklogDeferredArchive(repoID, inst)
	m.backgroundMutationWG.Wait()
	if len(*archived) != 1 || inst.OwedArchive() != nil {
		t.Fatalf("archived %v owed %+v", *archived, inst.OwedArchive())
	}
}

func TestAnswerBeforeFencedGuardCancelsQuietly(t *testing.T) {
	m, repoID, inst := startedItem(t)
	askInItem(t, m, repoID, inst, backlog.ThenArchive)
	idleItemSession(t, inst)
	prev := archiveSessionForLifecycle
	archiveSessionForLifecycle = func(_ *Manager, _ ArchiveSessionRequest, guard sessionTeardownGuard) error {
		if _, err := backlog.Update(repoID, func(b *backlog.Backlog) error {
			_, err := b.Answer(1, 0, "postgres", true, time.Now())
			return err
		}); err != nil {
			t.Fatal(err)
		}
		inst.ArmBacklogRun(time.Now())
		return guard(inst)
	}
	t.Cleanup(func() { archiveSessionForLifecycle = prev })
	m.backlogRunEnded(repoID, inst.ID)
	it := loadItem(t, repoID, 1)
	for _, msg := range it.Thread {
		if strings.HasPrefix(msg.Text, "Kept the session live") {
			t.Fatalf("note after an answer: %+v", msg)
		}
	}
	if inst.OwedArchive() != nil {
		t.Fatal("owed archive kept")
	}
}

func TestAnswerRestoresAfterArchiveFencedSend(t *testing.T) {
	m, repoID, inst := askedItem(t)
	held, resume := stubAnswerWait(t)
	sent := stubBacklogSend(t, m, repoID)
	sent.err = session.ErrAdoptionFenced
	needs := false
	prevNeeds := backlogNeedsRestore
	backlogNeedsRestore = func(itemSession) bool { return needs }
	t.Cleanup(func() { backlogNeedsRestore = prevNeeds })
	restores := 0
	prevRestore := restoreBacklogSession
	restoreBacklogSession = func(*Manager, RestoreSessionRequest) (session.InstanceData, error) {
		restores++
		needs = false
		return session.InstanceData{}, nil
	}
	t.Cleanup(func() { restoreBacklogSession = prevRestore })

	msg := answer(t, m, repoID, "postgres")
	<-held
	sent.mu.Lock()
	sent.err = nil
	sent.mu.Unlock()
	needs = true
	resume <- true
	m.backgroundMutationWG.Wait()

	if restores != 1 || sent.count() != 2 {
		t.Fatalf("restores %d sends %d", restores, sent.count())
	}
	it := loadItem(t, repoID, 1)
	for _, x := range it.Thread {
		if x.ID == msg.ID && x.Delivery != backlog.DeliveryDelivered {
			t.Fatalf("answer %+v", x)
		}
	}
	if !inst.BacklogRunActive() {
		t.Fatal("run not armed after the restore")
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
