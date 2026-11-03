package board

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"magma-viewer/internal/config"
	"magma-viewer/internal/fetcher"
	"magma-viewer/internal/store"
)

type Options struct {
	Root     string
	TempDir  string
	Magma    config.Magma
	Schedule string
	Shared   *fetcher.Credentials
	Alert    func(event, message string)
	Now      func() time.Time
}

type Runtime struct {
	ID      string
	Store   *store.Store
	Fetcher *fetcher.Fetcher

	ctx     context.Context
	cancel  context.CancelFunc
	entry   cron.EntryID
	version uint64
}

type Summary struct {
	Board
	Count    int       `json:"count"`
	Latest   time.Time `json:"latest,omitzero"`
	Bytes    int64     `json:"bytes"`
	Paused   bool      `json:"paused"`
	State    string    `json:"state"`
	Schedule string    `json:"effective_schedule"`
	NextRun  time.Time `json:"next_run,omitzero"`
}

// Manager owns active boards and their scheduled and background work.
type Manager struct {
	o        Options
	registry *Registry
	cron     *cron.Cron

	mu       sync.RWMutex
	runtimes map[string]*Runtime
	ctx      context.Context
	cancel   context.CancelFunc
	started  bool
	stopped  bool
	startup  sync.WaitGroup
	workers  sync.WaitGroup
}

func NewManager(o Options) (*Manager, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	r, err := Open(o.Root, o.Magma.URL, o.Now())
	if err != nil {
		return nil, err
	}
	if _, err := cron.ParseStandard(o.Schedule); err != nil {
		return nil, fmt.Errorf("default schedule: %w", err)
	}
	if o.Shared == nil {
		o.Shared = fetcher.NewCredentials("", filepath.Join(o.Root, ".state", "cookie.json"))
	}
	m := &Manager{
		o: o, registry: r,
		cron:     cron.New(cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger))),
		runtimes: make(map[string]*Runtime),
	}
	for _, b := range r.Snapshot().Boards {
		if b.Lifecycle != Active {
			continue
		}
		dir := BoardDir(o.Root, b.ID)
		info, err := os.Stat(dir)
		if err != nil {
			return nil, fmt.Errorf("board %s: %w", b.ID, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("board %s: storage is not a directory", b.ID)
		}
		rt, err := m.newRuntime(b)
		if err != nil {
			return nil, err
		}
		m.runtimes[b.ID] = rt
		m.schedule(rt, b)
	}
	return m, nil
}

func (m *Manager) newRuntime(b Board) (*Runtime, error) {
	st, err := store.New(BoardDir(m.o.Root, b.ID))
	if err != nil {
		return nil, fmt.Errorf("board %s: %w", b.ID, err)
	}
	cfg := m.o.Magma
	cfg.URL, cfg.Cookie = b.URL, ""
	f := fetcher.New(cfg, m.o.TempDir, st, m.o.Shared)
	f.URL = func() string {
		current, _ := m.registry.Board(b.ID)
		return current.URL
	}
	f.Active = func() bool {
		current, ok := m.registry.Board(b.ID)
		return ok && current.Lifecycle == Active
	}
	f.InWindow = func(now time.Time) bool {
		current, ok := m.registry.Board(b.ID)
		return ok && current.InWindow(now)
	}
	f.Alert = m.alert(b.ID)
	return &Runtime{ID: b.ID, Store: st, Fetcher: f}, nil
}

func (m *Manager) alert(id string) func(string, string) {
	if m.o.Alert == nil {
		return nil
	}
	return func(event, message string) {
		b, _ := m.registry.Board(id)
		m.o.Alert(event, fmt.Sprintf("[%s] %s", b.Name, message))
	}
}

func (m *Manager) effective(b Board) string {
	if b.Schedule != "" {
		return b.Schedule
	}
	return m.o.Schedule
}

// schedule is called under mu once the manager is published. All specs have
// already been validated, so installing an entry cannot fail after a commit.
func (m *Manager) schedule(rt *Runtime, b Board) {
	parsed, _ := cron.ParseStandard(m.effective(b))
	if rt.entry != 0 {
		m.cron.Remove(rt.entry)
	}
	rt.version++
	rt.entry = m.cron.Schedule(parsed, cron.FuncJob(m.job(rt.ID, rt.version)))
}

func (m *Manager) job(id string, version uint64) func() {
	return func() {
		m.mu.RLock()
		rt, ok := m.runtimes[id]
		if !ok || rt.version != version || !m.started || m.stopped {
			m.mu.RUnlock()
			return
		}
		ctx := rt.ctx
		m.mu.RUnlock()
		if ctx.Err() == nil {
			rt.Fetcher.RunScheduled(ctx)
		}
	}
}

func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started || m.stopped {
		return
	}
	m.started = true
	m.ctx, m.cancel = context.WithCancel(ctx)
	for _, rt := range m.runtimes {
		m.startRuntime(rt)
	}
	if m.o.Shared != nil && m.o.Alert != nil {
		m.workers.Go(func() { m.o.Shared.WatchToken(m.ctx.Done(), m.o.Alert) })
	}
	m.cron.Start()
}

// startRuntime is called under mu so Stop cannot race WaitGroup additions.
func (m *Manager) startRuntime(rt *Runtime) {
	rt.ctx, rt.cancel = context.WithCancel(m.ctx)
	m.startup.Go(func() { rt.Fetcher.RunScheduled(rt.ctx) })
	m.workers.Go(func() { rt.Store.Watch(rt.ctx.Done(), time.Minute) })
	m.workers.Go(func() {
		rt.Store.Cleanup(m.o.TempDir)
		rt.Store.Warm(rt.ctx.Done())
	})
	if rt.Fetcher.Alert != nil {
		m.workers.Go(func() { rt.Fetcher.WatchToken(rt.ctx.Done(), rt.Fetcher.Alert) })
	}
}

func (m *Manager) Stop() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	runtimes := m.snapshotLocked()
	cancel := m.cancel
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	for _, rt := range runtimes {
		if rt.cancel != nil {
			rt.cancel()
		}
	}
	<-m.cron.Stop().Done()
	m.startup.Wait()
	m.workers.Wait()
	for _, rt := range runtimes {
		rt.Fetcher.Drain()
	}
}

func (m *Manager) snapshotLocked() []*Runtime {
	runtimes := make([]*Runtime, 0, len(m.runtimes))
	for _, rt := range m.runtimes {
		runtimes = append(runtimes, rt)
	}
	return runtimes
}

func (m *Manager) LegacyID() string { return m.registry.Snapshot().LegacyBoardID }

func (m *Manager) Resolve(id string) (*Runtime, Board, error) {
	if id == "" {
		id = m.LegacyID()
	}
	b, ok := m.registry.Board(id)
	if !ok || b.Lifecycle != Active {
		return nil, Board{}, ErrNotFound
	}
	m.mu.RLock()
	rt := m.runtimes[id]
	m.mu.RUnlock()
	if rt == nil {
		return nil, Board{}, ErrNotFound
	}
	return rt, b, nil
}

func (m *Manager) summary(rt *Runtime, b Board) Summary {
	s := Summary{Board: b, Count: rt.Store.Count(), Bytes: rt.Store.Bytes(),
		Paused: rt.Fetcher.Status().Paused, Schedule: m.effective(b), NextRun: m.NextRun(b.ID)}
	if latest, ok := rt.Store.Latest(); ok {
		s.Latest = latest.Time
	}
	s.State = b.SyncState(m.o.Now(), s.Paused)
	return s
}

// Summary returns the current summary of an active board.
func (m *Manager) Summary(id string) (Summary, error) {
	rt, b, err := m.Resolve(id)
	if err != nil {
		return Summary{}, err
	}
	return m.summary(rt, b), nil
}

func (m *Manager) List(includeHidden bool) []Summary {
	result := make([]Summary, 0)
	for _, b := range m.All() {
		if b.Lifecycle != Active || (!includeHidden && b.Hidden) {
			continue
		}
		rt, _, err := m.Resolve(b.ID)
		if err == nil {
			result = append(result, m.summary(rt, b))
		}
	}
	slices.SortFunc(result, func(a, b Summary) int {
		if a.Latest.IsZero() != b.Latest.IsZero() {
			if a.Latest.IsZero() {
				return 1
			}
			return -1
		}
		if c := b.Latest.Compare(a.Latest); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Sort, b.Sort); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return result
}

func (m *Manager) Suggested(includeHidden bool) string {
	if boards := m.List(includeHidden); len(boards) != 0 {
		return boards[0].ID
	}
	return ""
}

func (m *Manager) Board(id string) (Board, bool) { return m.registry.Board(id) }

func (m *Manager) EffectiveSchedule(id string) string {
	b, ok := m.Board(id)
	if !ok {
		return ""
	}
	return m.effective(b)
}

func (m *Manager) NextRun(id string) time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if rt := m.runtimes[id]; rt != nil {
		return m.cron.Entry(rt.entry).Next
	}
	return time.Time{}
}

func (m *Manager) All() []Board { return m.registry.Snapshot().Boards }

func (m *Manager) Create(s Settings) (Summary, error) {
	s, err := s.Normalize()
	if err != nil {
		return Summary{}, err
	}
	// Mkdir claims the leaf exclusively; MkdirAll alone could adopt an album
	// left behind by another operation.
	if err := os.MkdirAll(filepath.Join(m.o.Root, "boards"), 0o755); err != nil {
		return Summary{}, err
	}
	for range 5 {
		id, err := NewID()
		if err != nil {
			return Summary{}, err
		}
		if _, exists := m.Board(id); exists {
			continue
		}
		dir := BoardDir(m.o.Root, id)
		if err := os.Mkdir(dir, 0o755); errors.Is(err, os.ErrExist) {
			continue
		} else if err != nil {
			return Summary{}, err
		}
		b := Board{ID: id, Settings: s, Lifecycle: Active, Created: m.o.Now().UTC()}
		rt, err := m.newRuntime(b)
		if err == nil {
			err = rt.Fetcher.SetPaused(true)
		}
		if err == nil {
			err = m.registry.Update(func(st *State) error {
				st.Boards = append(st.Boards, b)
				return nil
			})
		}
		if err != nil {
			_ = os.RemoveAll(dir)
			return Summary{}, err
		}
		m.mu.Lock()
		m.runtimes[id] = rt
		m.schedule(rt, b)
		if m.started && !m.stopped {
			m.startRuntime(rt)
		}
		m.mu.Unlock()
		return m.summary(rt, b), nil
	}
	return Summary{}, errors.New("could not allocate a unique board ID")
}

func (m *Manager) Update(id string, s Settings) (Summary, error) {
	rt, _, err := m.Resolve(id)
	if err != nil {
		return Summary{}, err
	}
	s, err = s.Normalize()
	if err != nil {
		return Summary{}, err
	}
	var before, after Board
	err = m.registry.Update(func(st *State) error {
		for i, b := range st.Boards {
			if b.ID != rt.ID || b.Lifecycle != Active {
				continue
			}
			before = b
			st.Boards[i].Settings = s
			after = st.Boards[i]
			return nil
		}
		return ErrNotFound
	})
	if err != nil {
		return Summary{}, err
	}
	if before.URL != after.URL {
		rt.Fetcher.ResetBackoff()
	}
	if m.effective(before) != m.effective(after) {
		m.mu.Lock()
		// Use the latest commit if another update completed in the meantime.
		current, _ := m.registry.Board(rt.ID)
		m.schedule(rt, current)
		m.mu.Unlock()
	}
	return m.summary(rt, after), nil
}

func (m *Manager) SetPaused(id string, paused bool) error {
	rt, _, err := m.Resolve(id)
	if err != nil {
		return err
	}
	return rt.Fetcher.SetPaused(paused)
}

func (m *Manager) SetSharedCookie(raw string) error {
	if err := m.o.Shared.Set(raw); err != nil {
		return err
	}
	m.resetBackoff()
	return nil
}

func (m *Manager) ClearSharedCookie() error {
	if err := m.o.Shared.Clear(); err != nil {
		return err
	}
	m.resetBackoff()
	return nil
}

func (m *Manager) resetBackoff() {
	m.mu.RLock()
	runtimes := m.snapshotLocked()
	m.mu.RUnlock()
	for _, rt := range runtimes {
		rt.Fetcher.ResetBackoff()
	}
}
