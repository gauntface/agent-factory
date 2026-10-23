package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sachiniyer/agent-factory/agentproto"
	"github.com/sachiniyer/agent-factory/backlog"
	"github.com/sachiniyer/agent-factory/config"
	"github.com/sachiniyer/agent-factory/internal/agentaccount"
	"github.com/sachiniyer/agent-factory/planusage"
	"github.com/sachiniyer/agent-factory/session"
	"github.com/sachiniyer/agent-factory/session/tmux"
)

const (
	// meterInterval is how often the poll loop meters transcripts, and the
	// least time between two backlog.spend events.
	meterInterval = 30 * time.Second
	// meterFreshFor is how old a pass may be before an on-demand read meters
	// again.
	meterFreshFor = 5 * time.Second
)

// meterState is the plan meter. mu is held for a whole pass, so passes never
// overlap; the last result and the poll loop's clock have their own locks so
// neither waits for a pass.
type meterState struct {
	mu        sync.Mutex
	published *planusage.SpendEvent
	lastSent  time.Time

	usageMu  sync.Mutex
	finished time.Time
	usage    planusage.Usage

	inFlight atomic.Bool
	startMu  sync.Mutex
	started  time.Time
}

func (s *meterState) due(now time.Time) bool {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if now.Sub(s.started) < meterInterval {
		return false
	}
	s.started = now
	return true
}

// MeterBacklog starts a metering pass on its own goroutine when one is due.
func (m *Manager) MeterBacklog() {
	st := &m.backlog.meter
	if !st.due(time.Now()) || !st.inFlight.CompareAndSwap(false, true) {
		return
	}
	if !m.launchBackgroundMutation(func(<-chan struct{}) {
		defer st.inFlight.Store(false)
		st.mu.Lock()
		defer st.mu.Unlock()
		m.meterPassLocked(time.Now())
	}) {
		st.inFlight.Store(false)
	}
}

// backlogUsage meters now unless a pass just finished or one is running, and
// returns the plan windows.
func (m *Manager) backlogUsage() planusage.Usage {
	st := &m.backlog.meter
	if usage, fresh := st.last(time.Now()); fresh {
		return usage
	}
	if st.mu.TryLock() {
		if _, admitted := m.beginBackgroundMutation(); admitted {
			m.meterPassLocked(time.Now())
			m.backgroundMutationWG.Done()
		}
		st.mu.Unlock()
	}
	usage, _ := st.last(time.Now())
	return usage
}

func (s *meterState) last(now time.Time) (planusage.Usage, bool) {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	return s.usage, !s.finished.IsZero() && now.Sub(s.finished) < meterFreshFor
}

// itemRun is one started run of an item, with its session when it still exists.
type itemRun struct {
	repoID string
	item   backlog.Item
	run    backlog.RunRef
	inst   *session.Instance
}

func (m *Manager) startedItemRuns(home string) []itemRun {
	paths, _ := filepath.Glob(filepath.Join(home, "repos", "*", backlog.BacklogFileName))
	if len(paths) == 0 {
		return nil
	}
	sessions := map[string]*session.Instance{}
	m.mu.Lock()
	for _, inst := range m.instances {
		if inst != nil && inst.BacklogItem != 0 {
			sessions[inst.ID] = inst
		}
	}
	m.mu.Unlock()
	var out []itemRun
	for _, p := range paths {
		repoID := filepath.Base(filepath.Dir(p))
		b, err := backlog.Load(repoID)
		if err != nil {
			m.warn().Printf("plan meter: backlog of project %s: %v", repoID, err)
			continue
		}
		for _, it := range b.Items {
			for _, r := range it.Runs {
				if r.State == backlog.RunStarted && r.SessionID != "" {
					out = append(out, itemRun{repoID: repoID, item: it, run: r, inst: sessions[r.SessionID]})
				}
			}
		}
	}
	return out
}

// meterPassLocked meters every item run and every Claude transcript, stores
// offsets and window totals, and publishes backlog.spend when it changed.
func (m *Manager) meterPassLocked(now time.Time) {
	st := &m.backlog.meter
	home, err := config.GetConfigDir()
	if err != nil {
		return
	}
	var overrides map[string]planusage.Price
	if cfg := m.Config(); cfg != nil {
		overrides = cfg.Backlog.Prices
	}
	usage := planusage.Usage{Runs: map[string]float64{}, Unmeterable: map[string]string{}}
	updates := map[string]map[string]map[string]backlog.Meter{}
	var backlogDirs []string
	for _, r := range m.startedItemRuns(home) {
		usage.Runs[r.run.SessionID] = r.run.Cost()
		running := r.item.Status == backlog.StatusRunning
		if r.inst == nil {
			continue
		}
		_, dirs, err := r.inst.ClaudeRunTranscriptDirs()
		if errors.Is(err, session.ErrNotClaude) {
			if running {
				program := r.inst.ResolvedAgent()
				if program == "" {
					program = r.inst.Program
				}
				usage.Unmeterable[r.run.SessionID] = backlog.UnmeterableProgram(r.item.ID, program)
			}
			continue
		}
		backlogDirs = append(backlogDirs, dirs...)
		rm := backlog.MeterRun(r.item.ID, r.run, planusage.TranscriptFiles(dirs), overrides, now)
		usage.Runs[r.run.SessionID] = rm.Cost()
		if running && rm.Unmeterable != "" {
			usage.Unmeterable[r.run.SessionID] = rm.Unmeterable
		}
		if rm.Changed {
			if updates[r.repoID] == nil {
				updates[r.repoID] = map[string]map[string]backlog.Meter{}
			}
			updates[r.repoID][r.run.SessionID] = rm.Meter
		}
	}
	for repoID, meters := range updates {
		if _, err := backlog.Update(repoID, func(b *backlog.Backlog) error {
			b.SetRunMeters(meters)
			return nil
		}); err != nil {
			m.warn().Printf("plan meter: storing run costs of project %s: %v", repoID, err)
		}
	}
	m.meterAccounts(home, backlogDirs, overrides, now, &usage)
	st.usageMu.Lock()
	st.usage, st.finished = usage, now
	st.usageMu.Unlock()
	spend := usage.SpendEvent()
	if (st.published == nil || !reflect.DeepEqual(*st.published, spend)) && now.Sub(st.lastSent) >= meterInterval {
		st.published, st.lastSent = &spend, now
		m.publishEvent(agentproto.EventBacklogSpend, spend)
	}
}

// meterAccounts meters every transcript under each Claude account's config
// dir and fills usage's windows from the account with the newest reading.
func (m *Manager) meterAccounts(home string, backlogDirs []string, overrides map[string]planusage.Price, now time.Time, usage *planusage.Usage) {
	sources := map[string]string{planusage.DefaultAccount: ambientClaudeConfigDir()}
	if names, err := agentaccount.List(home, tmux.ProgramClaude); err == nil {
		for _, name := range names {
			if acct, err := agentaccount.Selected(home, tmux.ProgramClaude, name); err == nil && acct.Dir != "" && name != planusage.DefaultAccount {
				sources[name] = acct.Dir
			}
		}
	}
	state, err := planusage.LoadMeter()
	if err != nil {
		m.warn().Printf("plan meter: %v", err)
		return
	}
	changed := false
	for name, dir := range sources {
		if dir != "" && state.Scan(name, dir, backlogDirs, overrides, now) {
			changed = true
		}
	}
	if changed {
		if err := state.Save(); err != nil {
			m.warn().Printf("plan meter: %v", err)
		}
	}
	var newest *planusage.Reading
	for name := range sources {
		r, err := planusage.LoadReading(name)
		if err != nil || r == nil || (newest != nil && !r.ReadAt.After(newest.ReadAt)) {
			continue
		}
		newest, usage.Account = r, name
	}
	if newest == nil {
		return
	}
	at := newest.ReadAt
	usage.ReadingAt = &at
	usage.Windows = planusage.Attribute(newest, state.Accounts[usage.Account], now)
}

func ambientClaudeConfigDir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude")
}
