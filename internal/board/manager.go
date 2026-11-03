package board

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	wg      sync.WaitGroup
	retired atomic.Bool
}

// Operation is the most recent maintenance result for a board.
type Operation struct {
	Kind     string    `json:"kind"`
	BoardID  string    `json:"board_id"`
	Phase    string    `json:"phase"`
	Done     int       `json:"done"`
	Total    int       `json:"total"`
	Err      string    `json:"error,omitempty"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitzero"`
}

var ErrMaintenanceBusy = errors.New("画板维护任务进行中")

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

	mu          sync.RWMutex
	runtimes    map[string]*Runtime
	ctx         context.Context
	cancel      context.CancelFunc
	started     bool
	stopped     bool
	startup     sync.WaitGroup
	workers     sync.WaitGroup
	operations  map[string]Operation
	busy        bool
	maintenance string
	stopParent  func() bool
	pending     []Board
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
		cron:       cron.New(cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger))),
		runtimes:   make(map[string]*Runtime),
		operations: make(map[string]Operation),
	}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	for _, b := range r.Snapshot().Boards {
		if b.Lifecycle != Active && b.Lifecycle != Archiving {
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
		if b.Lifecycle == Active {
			m.schedule(rt, b)
		}
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
	rt := &Runtime{ID: b.ID, Store: st, Fetcher: f}
	f.URL = func() string {
		current, _ := m.registry.Board(b.ID)
		return current.URL
	}
	f.Active = func() bool {
		current, ok := m.registry.Board(b.ID)
		return !rt.retired.Load() && ok && current.Lifecycle == Active
	}
	f.InWindow = func(now time.Time) bool {
		current, ok := m.registry.Board(b.ID)
		return ok && current.InWindow(now)
	}
	f.Alert = m.alert(b.ID)
	return rt, nil
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
		m.mu.Lock()
		rt, ok := m.runtimes[id]
		b, exists := m.registry.Board(id)
		if !ok || !exists || b.Lifecycle != Active || rt.version != version || !m.started || m.stopped {
			m.mu.Unlock()
			return
		}
		ctx := rt.ctx
		rt.wg.Add(1)
		m.mu.Unlock()
		defer rt.wg.Done()
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
	m.stopParent = context.AfterFunc(ctx, m.cancel)
	if ctx.Err() != nil {
		m.cancel()
	}
	for _, rt := range m.runtimes {
		b, _ := m.registry.Board(rt.ID)
		if b.Lifecycle == Active {
			m.startRuntime(rt)
		}
	}
	if m.o.Shared != nil && m.o.Alert != nil {
		m.workers.Go(func() { m.o.Shared.WatchToken(m.ctx.Done(), m.o.Alert) })
	}
	for _, b := range m.All() {
		if b.Lifecycle == Archived {
			if _, err := os.Lstat(BoardDir(m.o.Root, b.ID)); errors.Is(err, os.ErrNotExist) {
				continue
			}
		}
		if b.Lifecycle != Active && (!m.busy || b.ID != m.maintenance) {
			m.pending = append(m.pending, b)
		}
	}
	if len(m.pending) != 0 && !m.busy {
		b := m.pending[0]
		m.pending = m.pending[1:]
		m.startMaintenance(b)
	}
	m.cron.Start()
}

// startRuntime is called under mu so Stop cannot race WaitGroup additions.
func (m *Manager) startRuntime(rt *Runtime) {
	rt.ctx, rt.cancel = context.WithCancel(m.ctx)
	rt.wg.Add(1)
	m.startup.Go(func() { defer rt.wg.Done(); rt.Fetcher.RunScheduled(rt.ctx) })
	rt.wg.Go(func() { rt.Store.Watch(rt.ctx.Done(), time.Minute) })
	rt.wg.Go(func() {
		rt.Store.Cleanup(m.o.TempDir)
		rt.Store.Warm(rt.ctx.Done())
	})
	if rt.Fetcher.Alert != nil {
		rt.wg.Go(func() { rt.Fetcher.WatchToken(rt.ctx.Done(), rt.Fetcher.Alert) })
	}
}

// retire stops every producer before waiting for fetch and thumbnail work.
func (m *Manager) retire(rt *Runtime) {
	m.mu.Lock()
	rt.retired.Store(true)
	m.cron.Remove(rt.entry)
	rt.entry = 0
	rt.version++
	if rt.cancel != nil {
		rt.cancel()
	}
	m.mu.Unlock()
	rt.wg.Wait()
	rt.Fetcher.Drain()
}

func (m *Manager) Stop() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	runtimes := m.snapshotLocked()
	m.cancel()
	if m.stopParent != nil {
		m.stopParent()
	}
	m.mu.Unlock()
	<-m.cron.Stop().Done()
	m.workers.Wait()
	m.startup.Wait()
	for _, rt := range runtimes {
		rt.wg.Wait()
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
	m.mu.Lock()
	result, err := m.update(id, s)
	m.mu.Unlock()
	if err != nil {
		return Summary{}, err
	}
	return m.Summary(result.ID)
}

func (m *Manager) update(id string, s Settings) (Board, error) {
	if id == "" {
		id = m.LegacyID()
	}
	b, ok := m.registry.Board(id)
	rt := m.runtimes[id]
	if !ok || b.Lifecycle != Active || rt == nil {
		return Board{}, ErrNotFound
	}
	var err error
	s, err = s.Normalize()
	if err != nil {
		return Board{}, err
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
		return Board{}, err
	}
	if before.URL != after.URL {
		rt.Fetcher.ResetBackoff()
	}
	if m.effective(before) != m.effective(after) {
		current, _ := m.registry.Board(rt.ID)
		m.schedule(rt, current)
	}
	return after, nil
}

func (m *Manager) SetPaused(id string, paused bool) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if id == "" {
		id = m.LegacyID()
	}
	b, ok := m.registry.Board(id)
	rt := m.runtimes[id]
	if !ok || b.Lifecycle != Active || rt == nil {
		return ErrNotFound
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

// Operation returns a snapshot of the last maintenance attempt for id.
func (m *Manager) Operation(id string) (Operation, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	op, ok := m.operations[id]
	return op, ok
}

// beginOperation requires mu. The maintenance slot stays held through recovery.
func (m *Manager) beginOperation(kind, id string) {
	m.maintenance = id
	m.operations[id] = Operation{Kind: kind, BoardID: id, Phase: "retiring", Started: m.o.Now().UTC()}
}

// startMaintenance requires mu and keeps pending recovery work in the same slot.
func (m *Manager) startMaintenance(b Board) {
	kind := "archive"
	if b.Lifecycle == Deleting {
		kind = "delete"
	}
	m.busy = true
	m.beginOperation(kind, b.ID)
	m.workers.Go(func() {
		for {
			m.runMaintenance(b)
			m.mu.Lock()
			if len(m.pending) == 0 || m.ctx.Err() != nil {
				m.busy, m.maintenance = false, ""
				m.mu.Unlock()
				return
			}
			b = m.pending[0]
			m.pending = m.pending[1:]
			kind := "archive"
			if b.Lifecycle == Deleting {
				kind = "delete"
			}
			m.beginOperation(kind, b.ID)
			m.mu.Unlock()
		}
	})
}

func (m *Manager) progress(id, phase string, done, total int) {
	m.mu.Lock()
	op := m.operations[id]
	op.Phase, op.Done, op.Total = phase, done, total
	m.operations[id] = op
	m.mu.Unlock()
}

func (m *Manager) finish(id string, err error) {
	m.mu.Lock()
	op := m.operations[id]
	op.Phase, op.Finished = "done", m.o.Now().UTC()
	if err != nil {
		op.Phase, op.Err = "failed", err.Error()
		log.Printf("board: %s %s: %v", op.Kind, id, err)
	}
	m.operations[id] = op
	m.mu.Unlock()
}

func (m *Manager) lifecycle(id string, lifecycle Lifecycle) error {
	return m.registry.Update(func(st *State) error {
		for i := range st.Boards {
			if st.Boards[i].ID == id {
				st.Boards[i].Lifecycle = lifecycle
				return nil
			}
		}
		return ErrNotFound
	})
}

// Archive takes a board offline immediately and archives it in the background.
func (m *Manager) Archive(id string) error { return m.maintain(id, "", "archive") }

// Delete requires the board name as confirmation before starting deletion.
func (m *Manager) Delete(id, confirm string) error { return m.maintain(id, confirm, "delete") }

func (m *Manager) maintain(id, confirm, kind string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.busy {
		return ErrMaintenanceBusy
	}
	if m.stopped || m.ctx.Err() != nil {
		return context.Canceled
	}
	b, ok := m.registry.Board(id)
	if !ok || (b.Lifecycle != Active && (kind != "delete" || b.Lifecycle != Archived)) {
		return ErrNotFound
	}
	if kind == "delete" && strings.TrimSpace(confirm) != b.Name {
		return InvalidError{"confirm_mismatch", "确认名称不一致"}
	}
	lifecycle := Archiving
	if kind == "delete" {
		lifecycle = Deleting
	}
	if err := m.lifecycle(id, lifecycle); err != nil {
		return err
	}
	b.Lifecycle = lifecycle
	m.startMaintenance(b)
	return nil
}

func (m *Manager) runMaintenance(b Board) {
	m.mu.RLock()
	rt := m.runtimes[b.ID]
	m.mu.RUnlock()
	if rt != nil {
		m.retire(rt)
	}
	var err error
	switch b.Lifecycle {
	case Archiving:
		err = m.archive(b, rt)
	case Archived:
		m.progress(b.ID, "removing", 0, 0)
		err = removeBoardDir(m.ctx, BoardDir(m.o.Root, b.ID))
	case Deleting:
		err = m.deleteBoard(b.ID)
	}
	if err == nil || b.Lifecycle == Archived {
		m.mu.Lock()
		delete(m.runtimes, b.ID)
		m.mu.Unlock()
	}
	m.finish(b.ID, err)
}

func (m *Manager) archive(b Board, rt *Runtime) error {
	err := m.cleanArchives(b.ID, true)
	var a Archive
	if err == nil {
		if rt == nil {
			err = errors.New("missing archive runtime")
		} else {
			a, err = buildArchive(m.ctx, filepath.Join(m.o.Root, "archives"), b, rt.Store, m.o.Now(), func(done, total int) {
				m.progress(b.ID, "writing", done, total)
			})
		}
	}
	if err == nil {
		a.ID, err = NewArchiveID()
	}
	if err == nil {
		err = m.ctx.Err()
	}
	if err == nil {
		m.progress(b.ID, "committing", a.Images, a.Images)
		err = m.registry.Update(func(st *State) error {
			for i := range st.Boards {
				if st.Boards[i].ID == b.ID && st.Boards[i].Lifecycle == Archiving {
					st.Boards[i].Lifecycle = Archived
					st.Archives = append(st.Archives, a)
					return nil
				}
			}
			return ErrNotFound
		})
	}
	if err != nil {
		if a.File != "" {
			err = errors.Join(err, removeArchiveFile(filepath.Join(m.o.Root, "archives", a.File)))
		}
		// Cancellation leaves Archiving for the next Start; the sources stay intact.
		if m.ctx.Err() != nil {
			return err
		}
		return errors.Join(err, m.restoreActive(b, rt))
	}
	m.progress(b.ID, "removing", a.Images, a.Images)
	err = removeBoardDir(m.ctx, BoardDir(m.o.Root, b.ID))
	// The registry commit makes the archive authoritative even if cleanup fails.
	m.mu.Lock()
	delete(m.runtimes, b.ID)
	m.mu.Unlock()
	return err
}

func (m *Manager) restoreActive(b Board, old *Runtime) error {
	rt, err := m.newRuntime(b)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped || m.ctx.Err() != nil {
		return m.ctx.Err()
	}
	if err := m.lifecycle(b.ID, Active); err != nil {
		return err
	}
	if old != nil {
		rt.version = old.version
	}
	m.runtimes[b.ID] = rt
	m.schedule(rt, b)
	if m.started {
		m.startRuntime(rt)
	}
	return nil
}

func removeBoardDir(ctx context.Context, dir string) error {
	var err error
	for attempt := range 10 {
		if err := ctx.Err(); err != nil {
			return err
		}
		err = os.RemoveAll(dir)
		if err == nil {
			return nil
		}
		if attempt < 9 {
			select {
			case <-ctx.Done():
				return errors.Join(err, ctx.Err())
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	return err
}

func removeArchiveFile(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// cleanArchives preserves registered files when recovering an interrupted build.
func (m *Manager) cleanArchives(id string, unregisteredOnly bool) error {
	dir := filepath.Join(m.o.Root, "archives")
	registered := make(map[string]bool)
	for _, a := range m.registry.Snapshot().Archives {
		if a.BoardID != id {
			continue
		}
		registered[a.File] = true
		if !unregisteredOnly {
			if err := removeArchiveFile(filepath.Join(dir, a.File)); err != nil {
				return err
			}
		}
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		zipFile := strings.HasPrefix(name, id+"--") && strings.HasSuffix(name, ".zip")
		partFile := strings.HasPrefix(name, "."+id+"--") && strings.HasSuffix(name, ".part")
		if (!zipFile && !partFile) || (unregisteredOnly && registered[name]) {
			continue
		}
		if err := removeArchiveFile(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) deleteBoard(id string) error {
	m.progress(id, "removing", 0, 0)
	if err := removeBoardDir(m.ctx, BoardDir(m.o.Root, id)); err != nil {
		return err
	}
	if err := m.cleanArchives(id, false); err != nil {
		return err
	}
	if err := m.ctx.Err(); err != nil {
		return err
	}
	return m.registry.Update(func(st *State) error {
		st.Boards = slices.DeleteFunc(st.Boards, func(b Board) bool { return b.ID == id })
		st.Archives = slices.DeleteFunc(st.Archives, func(a Archive) bool { return a.BoardID == id })
		return nil
	})
}

// Archives returns all registered archives, newest first.
func (m *Manager) Archives() []Archive {
	archives := m.registry.Snapshot().Archives
	slices.SortFunc(archives, func(a, b Archive) int { return b.Created.Compare(a.Created) })
	return archives
}

func (m *Manager) archiveFile(id string) (string, Archive, error) {
	for _, a := range m.registry.Snapshot().Archives {
		if a.ID != id {
			continue
		}
		if m.busy && m.maintenance == a.BoardID {
			return "", Archive{}, ErrMaintenanceBusy
		}
		return filepath.Join(m.o.Root, "archives", a.File), a, nil
	}
	return "", Archive{}, ErrNotFound
}

// ArchiveFile resolves a registered archive inside the archives directory.
func (m *Manager) ArchiveFile(archiveID string) (path string, a Archive, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	path, a, err = m.archiveFile(archiveID)
	if err != nil {
		return "", Archive{}, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return "", Archive{}, ErrNotFound
	} else if err != nil {
		return "", Archive{}, err
	}
	return path, a, nil
}

// DeleteArchive removes an archive and drops an archived board's final record.
func (m *Manager) DeleteArchive(archiveID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	path, a, err := m.archiveFile(archiveID)
	if err != nil {
		return err
	}
	if b, ok := m.registry.Board(a.BoardID); ok && b.Lifecycle == Archived {
		// Finish any post-commit directory cleanup before removing its recovery marker.
		if err := removeBoardDir(m.ctx, BoardDir(m.o.Root, b.ID)); err != nil {
			return err
		}
	}
	if err := removeArchiveFile(path); err != nil {
		return err
	}
	return m.registry.Update(func(st *State) error {
		st.Archives = slices.DeleteFunc(st.Archives, func(a Archive) bool { return a.ID == archiveID })
		if !slices.ContainsFunc(st.Archives, func(other Archive) bool { return other.BoardID == a.BoardID }) {
			st.Boards = slices.DeleteFunc(st.Boards, func(b Board) bool { return b.ID == a.BoardID && b.Lifecycle == Archived })
		}
		return nil
	})
}
