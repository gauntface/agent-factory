package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
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
	// meterFreshFor is how old a result may be before an on-demand read
	// starts a pass in the background.
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
	if m.backlog.meter.due(time.Now()) {
		m.startMeterPass()
	}
}

// startMeterPass runs a metering pass on its own goroutine unless one is
// already running.
func (m *Manager) startMeterPass() {
	st := &m.backlog.meter
	if !st.inFlight.CompareAndSwap(false, true) {
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

// backlogUsage returns the last metering result at once. A stale result
// starts a pass in the background; before the first pass ends the result says
// it is still metering. A request never reads transcripts itself: the first
// pass can read a week of them.
func (m *Manager) backlogUsage() planusage.Usage {
	st := &m.backlog.meter
	usage, fresh := st.last(time.Now())
	if !fresh {
		m.startMeterPass()
	}
	return usage
}

func (s *meterState) last(now time.Time) (planusage.Usage, bool) {
	u, at := s.result()
	if at.IsZero() {
		return u, false
	}
	return u, now.Sub(at) < meterFreshFor
}

// result is the last pass's usage, however old, and when it finished; zero
// before the first pass.
func (s *meterState) result() (planusage.Usage, time.Time) {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	if s.finished.IsZero() {
		return planusage.Usage{Metering: true, Runs: map[string]float64{}, RunWindow: map[string]float64{}}, time.Time{}
	}
	return s.usage, s.finished
}

// current is the last pass's usage, however old.
func (s *meterState) current() planusage.Usage {
	u, _ := s.result()
	return u
}

// meterUsageWithin meters on the caller's goroutine unless the last pass
// finished within age, then returns the usage. A pass already running is
// waited for. Only background work calls it: a pass can read a week of
// transcripts.
func (m *Manager) meterUsageWithin(age time.Duration) planusage.Usage {
	st := &m.backlog.meter
	if u, at := st.result(); !at.IsZero() && time.Since(at) < age {
		return u
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if u, at := st.result(); !at.IsZero() && time.Since(at) < age {
		return u
	}
	if _, admitted := m.beginBackgroundMutation(); admitted {
		m.meterPassLocked(time.Now())
		m.backgroundMutationWG.Done()
	}
	return st.current()
}

// itemRun is one started run of an item, with its session when it still exists.
type itemRun struct {
	repoID string
	item   backlog.Item
	run    backlog.RunRef
	inst   *session.Instance
}

// orchRun is a group's orchestrator, with its session when it still exists.
type orchRun struct {
	repoID string
	group  int
	orch   backlog.Orchestrator
	inst   *session.Instance
}

// startedItemRuns returns every started item run and every bound
// orchestrator across projects.
func (m *Manager) startedItemRuns(home string) ([]itemRun, []orchRun) {
	paths, _ := filepath.Glob(filepath.Join(home, "repos", "*", backlog.BacklogFileName))
	if len(paths) == 0 {
		return nil, nil
	}
	sessions := map[string]*session.Instance{}
	m.mu.Lock()
	for _, inst := range m.instances {
		if inst != nil {
			sessions[inst.ID] = inst
		}
	}
	m.mu.Unlock()
	var out []itemRun
	var orchs []orchRun
	for _, p := range paths {
		repoID := filepath.Base(filepath.Dir(p))
		b, err := backlog.Load(repoID)
		if err != nil {
			m.warn().Printf("plan meter: backlog of project %s: %v", repoID, err)
			continue
		}
		for _, it := range b.Items {
			if o := it.Orchestrator; o != nil && o.SessionID != "" {
				var inst *session.Instance
				if s := sessions[o.SessionID]; s != nil && s.BacklogItem == 0 {
					inst = s
				}
				orchs = append(orchs, orchRun{repoID: repoID, group: it.ID, orch: *o, inst: inst})
			}
			for _, r := range it.Runs {
				if r.State == backlog.RunStarted && r.SessionID != "" {
					inst := sessions[r.SessionID]
					if inst != nil && inst.BacklogItem == 0 {
						inst = nil
					}
					out = append(out, itemRun{repoID: repoID, item: it, run: r, inst: inst})
				}
			}
		}
	}
	return out, orchs
}

// meterPassLocked meters every item run, every orchestrator, and every Claude
// transcript; orchestrator transcripts count as backlog spend. It stores
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
	state, err := planusage.LoadMeter()
	if err != nil {
		m.warn().Printf("plan meter: %v", err)
		return
	}
	usage := planusage.Usage{Runs: map[string]float64{}, RunWindow: map[string]float64{}, Unmeterable: map[string]string{}, Accounts: map[string]planusage.AccountUsage{}}
	runAccount := map[string]string{}
	updates := map[string]map[string]map[string]backlog.Meter{}
	var backlogDirs []string
	metered := map[string]bool{}
	runs, orchs := m.startedItemRuns(home)
	launchDirs := map[string]map[string]string{}
	projects := map[string]string{}
	for _, r := range runs {
		usage.Runs[r.run.SessionID] = r.run.Cost()
		if r.inst == nil {
			continue
		}
		archived := r.inst.IsArchived()
		running := r.item.Status == backlog.StatusRunning && !archived && r.inst.BacklogRunEndedAt().IsZero()
		project, ok := projects[r.repoID]
		if !ok {
			project = backlogHost{m: m}.ProjectName(r.repoID)
			projects[r.repoID] = project
		}
		workDirs := runLaunchDirs(r.run, r.inst.GetWorktreePath(), archived)
		configDir, dirs, err := runTranscriptDirs(r.inst, workDirs)
		if errors.Is(err, session.ErrNotClaude) {
			if running {
				program := r.inst.ResolvedAgent()
				if program == "" {
					program = r.inst.Program
				}
				usage.Unmeterable[r.run.SessionID] = backlog.UnmeterableProgram(r.item.ID, project, program)
			}
			continue
		}
		if r.run.LaunchDir == "" && !archived && len(workDirs) > 0 {
			if launchDirs[r.repoID] == nil {
				launchDirs[r.repoID] = map[string]string{}
			}
			launchDirs[r.repoID][r.run.SessionID] = workDirs[0]
		}
		backlogDirs = append(backlogDirs, dirs...)
		metered[r.run.SessionID] = true
		seen := state.RunSeenFor(r.run.SessionID)
		planusage.PruneSeen(seen, now)
		files := planusage.TranscriptFiles(dirs)
		if !running {
			files = withMeteredFiles(files, r.run)
		}
		rm := backlog.MeterRun(r.item.ID, project, r.run, files, seen, overrides, now)
		usage.Runs[r.run.SessionID] = rm.Cost()
		state.AddRunEntries(r.run.SessionID, rm.Entries)
		runAccount[r.run.SessionID] = planusage.AccountForConfigDir(home, configDir)
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
	orchUpdates := map[string]map[string]map[string]backlog.Meter{}
	for _, r := range orchs {
		if r.inst == nil {
			continue
		}
		conv, dirs, ok := orchestratorConversation(r.inst)
		if !ok {
			continue
		}
		backlogDirs = append(backlogDirs, planusage.ConversationPaths(dirs, conv)...)
		sessionID := r.orch.SessionID
		metered[sessionID] = true
		seen := state.RunSeenFor(sessionID)
		planusage.PruneSeen(seen, now)
		rm := backlog.MeterOrchestrator(r.group, r.orch, planusage.ConversationFiles(dirs, conv), seen, overrides)
		if rm.Changed {
			if orchUpdates[r.repoID] == nil {
				orchUpdates[r.repoID] = map[string]map[string]backlog.Meter{}
			}
			orchUpdates[r.repoID][sessionID] = rm.Meter
		}
	}
	repos := map[string]bool{}
	for repoID := range updates {
		repos[repoID] = true
	}
	for repoID := range orchUpdates {
		repos[repoID] = true
	}
	for repoID := range launchDirs {
		repos[repoID] = true
	}
	for repoID := range repos {
		if _, err := backlog.Update(repoID, func(b *backlog.Backlog) error {
			b.SetRunMeters(updates[repoID])
			b.SetRunLaunchDirs(launchDirs[repoID])
			b.SetOrchestratorMeters(orchUpdates[repoID])
			return nil
		}); err != nil {
			m.warn().Printf("plan meter: storing run costs of project %s: %v", repoID, err)
		}
	}
	state.KeepRunSeen(metered)
	state.PruneRunBuckets(now)
	m.meterAccounts(home, state, backlogDirs, overrides, now, &usage)
	for sessionID, account := range runAccount {
		usage.RunWindow[sessionID] = state.RunWindowCost(sessionID, usage.Accounts[account].Windows.FiveHour)
	}
	if err := state.Save(); err != nil {
		m.warn().Printf("plan meter: %v", err)
	}
	st.usageMu.Lock()
	st.usage, st.finished = usage, now
	st.usageMu.Unlock()
	spend := usage.SpendEvent()
	if (st.published == nil || !reflect.DeepEqual(*st.published, spend)) && now.Sub(st.lastSent) >= meterInterval {
		st.published, st.lastSent = &spend, now
		m.publishEvent(agentproto.EventBacklogSpend, spend)
	}
}

// runLaunchDirs are the directories Claude may have been launched in for a
// run: where the run first launched, and where its session is now unless it is
// archived, since an archived worktree is not where Claude ran.
func runLaunchDirs(run backlog.RunRef, current string, archived bool) []string {
	var out []string
	if !archived && current != "" {
		out = append(out, current)
	}
	if run.LaunchDir != "" && (archived || run.LaunchDir != current) {
		out = append(out, run.LaunchDir)
	}
	return out
}

// runTranscriptDirs joins the transcript dirs of every launch dir.
func runTranscriptDirs(inst *session.Instance, workDirs []string) (string, []string, error) {
	if len(workDirs) == 0 {
		if inst.ResolvedAgent() != tmux.ProgramClaude {
			return "", nil, session.ErrNotClaude
		}
		return "", nil, nil
	}
	var configDir string
	var dirs []string
	var firstErr error
	for _, wd := range workDirs {
		cd, d, err := inst.ClaudeRunTranscriptDirsIn(wd)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		configDir = cd
		for _, p := range d {
			if !slices.Contains(dirs, p) {
				dirs = append(dirs, p)
			}
		}
	}
	if len(dirs) == 0 && firstErr != nil {
		return "", nil, firstErr
	}
	return configDir, dirs, nil
}

// withMeteredFiles adds the transcript files a run already metered that still
// exist, for a run whose launch dir is unknown.
func withMeteredFiles(files []string, run backlog.RunRef) []string {
	for f := range run.Meter {
		if _, err := os.Stat(f); err == nil && !slices.Contains(files, f) {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	return files
}

// orchestratorConversation is the Claude conversation id of an orchestrator
// session and the project dirs its transcripts are in. Only that conversation
// is metered: the dirs also hold every other Claude session started in the
// same directory, the user's own included when the session runs in place.
func orchestratorConversation(inst *session.Instance) (string, []string, bool) {
	conv := inst.AgentConversation()
	if !conv.HasID() || conv.Agent != tmux.ProgramClaude {
		return "", nil, false
	}
	_, dirs, err := inst.ClaudeRunTranscriptDirs()
	if err != nil || len(dirs) == 0 {
		return "", nil, false
	}
	return strings.TrimSpace(conv.ID), dirs, true
}

// meterAccounts meters every transcript under each Claude account's config
// dir and fills usage with each account's windows; the top-level windows are
// the account with the newest reading.
func (m *Manager) meterAccounts(home string, state *planusage.MeterState, backlogDirs []string, overrides map[string]planusage.Price, now time.Time, usage *planusage.Usage) {
	sources := map[string]string{planusage.DefaultAccount: ambientClaudeConfigDir()}
	if names, err := agentaccount.List(home, tmux.ProgramClaude); err == nil {
		for _, name := range names {
			if acct, err := agentaccount.Selected(home, tmux.ProgramClaude, name); err == nil && acct.Dir != "" && name != planusage.DefaultAccount {
				sources[name] = acct.Dir
			}
		}
	}
	for name, dir := range sources {
		if dir != "" {
			state.Scan(name, dir, backlogDirs, overrides, now)
		}
		r, err := planusage.LoadReading(name)
		if err != nil {
			m.warn().Printf("plan meter: reading of account %s: %v", name, err)
		}
		au := planusage.AccountUsageOf(r, state.Accounts[name], overrides, now)
		planusage.SetBacklogCosts(&au.Windows, state.Accounts[name], now)
		usage.Accounts[name] = au
		if au.ReadingAt != nil && (usage.ReadingAt == nil || au.ReadingAt.After(*usage.ReadingAt)) {
			usage.Account, usage.Windows, usage.ReadingAt = name, au.Windows, au.ReadingAt
		}
	}
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
