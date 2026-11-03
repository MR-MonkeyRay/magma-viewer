package board

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"magma-viewer/internal/config"
	"magma-viewer/internal/fetcher"
)

func managerPNG(t *testing.T, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for x := range 2 {
		for y := range 2 {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func managerOptions(root, rawURL string) Options {
	return Options{
		Root:     root,
		TempDir:  root,
		Magma:    config.Magma{URL: rawURL, Timeout: 2 * time.Second},
		Schedule: "@every 1h",
		Shared:   fetcher.NewCredentials("", filepath.Join(root, ".state", "cookie.json")),
	}
}

func newTestManager(t *testing.T, root, rawURL string) *Manager {
	t.Helper()
	m, err := NewManager(managerOptions(root, rawURL))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	return m
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !ok() {
		t.Fatal("condition was not met")
	}
}

func TestManagerMigratesLegacyStore(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "legacy.png"), managerPNG(t, color.White), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newTestManager(t, root, "https://example.com/render")
	if m.LegacyID() != LegacyID {
		t.Fatalf("LegacyID = %q", m.LegacyID())
	}
	rt, _, err := m.Resolve("")
	if err != nil || rt.Store.Count() != 1 {
		t.Fatalf("Resolve legacy = %v, count %d", err, rt.Store.Count())
	}
}

func TestManagerCreatePersistsPausedBoard(t *testing.T) {
	root := t.TempDir()
	m := newTestManager(t, root, "")
	s, err := m.Create(Settings{Name: " New ", URL: "https://example.com/a"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "New" || !s.Paused || s.Lifecycle != Active {
		t.Fatalf("created summary = %+v", s)
	}
	if info, err := os.Stat(BoardDir(root, s.ID)); err != nil || !info.IsDir() {
		t.Fatalf("board directory: %v, %v", info, err)
	}

	reloaded := newTestManager(t, root, "")
	rt, b, err := reloaded.Resolve(s.ID)
	if err != nil || b.Name != "New" || !rt.Fetcher.Status().Paused {
		t.Fatalf("reloaded board = %+v, paused=%v, err=%v", b, rt.Fetcher.Status().Paused, err)
	}
	before, err := os.ReadDir(filepath.Join(root, "boards"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Create(Settings{Name: "broken", URL: "not a URL"})
	var invalid InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("Create invalid error = %v", err)
	}
	after, err := os.ReadDir(filepath.Join(root, "boards"))
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("invalid Create left a directory: before=%d after=%d", len(before), len(after))
	}
}

func TestManagerPersistenceFailuresKeepCommittedURL(t *testing.T) {
	var oldHits, newHits atomic.Int32
	serve := func(hits *atomic.Int32, c color.Color) *httptest.Server {
		body := managerPNG(t, c)
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			_, _ = w.Write(body)
		}))
	}
	oldServer := serve(&oldHits, color.White)
	defer oldServer.Close()
	newServer := serve(&newHits, color.Black)
	defer newServer.Close()

	root := t.TempDir()
	m := newTestManager(t, root, "")
	created, err := m.Create(Settings{Name: "kept", URL: oldServer.URL})
	if err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	originalPath := m.registry.path
	m.registry.path = filepath.Join(blocker, "boards.json")
	before, err := os.ReadDir(filepath.Join(root, "boards"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(Settings{Name: "discarded", URL: oldServer.URL}); err == nil {
		t.Fatal("Create succeeded with an unwritable registry")
	}
	after, err := os.ReadDir(filepath.Join(root, "boards"))
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("failed Create left a directory: before=%d after=%d", len(before), len(after))
	}
	if _, err := m.Update(created.ID, Settings{Name: "changed", URL: newServer.URL}); err == nil {
		t.Fatal("Update succeeded with an unwritable registry")
	}
	m.registry.path = originalPath
	rt, _, _ := m.Resolve(created.ID)
	got, err := rt.Fetcher.Run(context.Background())
	if err != nil || got.Result != fetcher.ResultSaved {
		t.Fatalf("Run after failed update = %+v, %v", got, err)
	}
	if oldHits.Load() != 1 || newHits.Load() != 0 {
		t.Fatalf("failed update changed URL: old=%d new=%d", oldHits.Load(), newHits.Load())
	}
}

func TestManagerUpdateScheduleAndURL(t *testing.T) {
	var first, second atomic.Int32
	serve := func(n *atomic.Int32, c color.Color) *httptest.Server {
		body := managerPNG(t, c)
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			n.Add(1)
			_, _ = w.Write(body)
		}))
	}
	a := serve(&first, color.White)
	defer a.Close()
	b := serve(&second, color.Black)
	defer b.Close()

	m := newTestManager(t, t.TempDir(), "")
	created, err := m.Create(Settings{Name: "one", URL: a.URL})
	if err != nil {
		t.Fatal(err)
	}
	m.Start(context.Background())
	t.Cleanup(m.Stop)
	m.startup.Wait()
	before := m.NextRun(created.ID)
	updated, err := m.Update(created.ID, Settings{Name: "two", URL: a.URL, Schedule: "@every 2h"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "two" || m.EffectiveSchedule(created.ID) != "@every 2h" {
		t.Fatalf("updated = %+v, schedule=%q", updated, m.EffectiveSchedule(created.ID))
	}
	overridden := m.NextRun(created.ID)
	if before.IsZero() || overridden.IsZero() || before.Equal(overridden) {
		t.Fatalf("NextRun did not change: %v -> %v", before, overridden)
	}
	if _, err := m.Update(created.ID, Settings{Name: "two", URL: b.URL}); err != nil {
		t.Fatal(err)
	}
	if m.EffectiveSchedule(created.ID) != "@every 1h" {
		t.Fatalf("inherited schedule = %q", m.EffectiveSchedule(created.ID))
	}
	inherited := m.NextRun(created.ID)
	if inherited.IsZero() || inherited.Equal(overridden) {
		t.Fatalf("NextRun did not return to inherited schedule: %v -> %v", overridden, inherited)
	}
	rt, _, _ := m.Resolve(created.ID)
	got, err := rt.Fetcher.Run(context.Background())
	if err != nil || got.Result != fetcher.ResultSaved {
		t.Fatalf("Run = %+v, %v", got, err)
	}
	if first.Load() != 0 || second.Load() != 1 {
		t.Fatalf("requests: first=%d second=%d", first.Load(), second.Load())
	}
}

func TestManagerUpdateURLDuringFetch(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	body := managerPNG(t, color.White)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	defer close(release)
	var newHits atomic.Int32
	newServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		newHits.Add(1)
		_, _ = w.Write(body)
	}))
	defer newServer.Close()
	m := newTestManager(t, t.TempDir(), "")
	created, err := m.Create(Settings{Name: "old", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	rt, _, _ := m.Resolve(created.ID)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = rt.Fetcher.Run(context.Background())
	}()
	<-entered
	_, err = m.Update(created.ID, Settings{Name: "new", URL: newServer.URL})
	if err != nil {
		t.Fatalf("Update error = %v", err)
	}
	if got, _ := m.Board(created.ID); got.Name != "new" || got.URL != newServer.URL {
		t.Fatalf("registry did not commit during fetch: %+v", got)
	}
	release <- struct{}{}
	<-done
	if got, err := rt.Fetcher.Run(context.Background()); err != nil || got.Result != fetcher.ResultUnchanged {
		t.Fatalf("Run after URL update = %+v, %v", got, err)
	}
	if newHits.Load() != 1 {
		t.Fatalf("new URL received %d requests", newHits.Load())
	}
}

func TestManagerURLUpdateResetsBackoffOnlyAfterCommit(t *testing.T) {
	var newHits atomic.Int32
	body := managerPNG(t, color.White)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		newHits.Add(1)
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	root := t.TempDir()
	m := newTestManager(t, root, "")
	created, err := m.Create(Settings{Name: "board", URL: srv.URL + "/old"})
	if err != nil {
		t.Fatal(err)
	}
	rt, _, _ := m.Resolve(created.ID)
	for range 3 {
		if _, err := rt.Fetcher.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	retryAt := rt.Fetcher.Status().RetryAt
	if retryAt.IsZero() {
		t.Fatal("test did not establish a backoff")
	}
	if _, err := m.Update(created.ID, Settings{Name: "renamed", URL: created.URL}); err != nil {
		t.Fatal(err)
	}
	if got := rt.Fetcher.Status().RetryAt; !got.Equal(retryAt) {
		t.Fatalf("unchanged URL reset backoff: %v -> %v", retryAt, got)
	}
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	originalPath := m.registry.path
	m.registry.path = filepath.Join(blocker, "boards.json")
	settings := Settings{Name: "updated", URL: srv.URL + "/new"}
	if _, err := m.Update(created.ID, settings); err == nil {
		t.Fatal("Update succeeded with an unwritable registry")
	}
	if got := rt.Fetcher.Status().RetryAt; !got.Equal(retryAt) {
		t.Fatalf("failed update reset backoff: %v -> %v", retryAt, got)
	}
	m.registry.path = originalPath
	if _, err := m.Update(created.ID, settings); err != nil {
		t.Fatal(err)
	}
	if got := rt.Fetcher.Status().RetryAt; !got.IsZero() {
		t.Fatalf("committed URL update kept backoff: %v", got)
	}
	if err := m.SetPaused(created.ID, false); err != nil {
		t.Fatal(err)
	}
	rt.Fetcher.RunScheduled(context.Background())
	if newHits.Load() != 1 {
		t.Fatalf("new URL received %d requests", newHits.Load())
	}
}

func TestManagerRejectsStaleCronJob(t *testing.T) {
	var hits atomic.Int32
	body := managerPNG(t, color.White)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	m := newTestManager(t, t.TempDir(), "")
	created, err := m.Create(Settings{Name: "board", URL: srv.URL, Schedule: "@every 1h"})
	if err != nil {
		t.Fatal(err)
	}
	m.Start(context.Background())
	t.Cleanup(m.Stop)
	m.startup.Wait()
	if err := m.SetPaused(created.ID, false); err != nil {
		t.Fatal(err)
	}
	m.mu.RLock()
	old := m.runtimes[created.ID].version
	m.mu.RUnlock()
	stale := m.job(created.ID, old)
	if _, err := m.Update(created.ID, Settings{Name: "board", URL: srv.URL, Schedule: "@every 2h"}); err != nil {
		t.Fatal(err)
	}
	stale()
	if hits.Load() != 0 {
		t.Fatalf("stale job sent %d requests", hits.Load())
	}
	m.mu.RLock()
	currentVersion := m.runtimes[created.ID].version
	m.mu.RUnlock()
	current := m.job(created.ID, currentVersion)
	current()
	if hits.Load() != 1 {
		t.Fatalf("current job sent %d requests, want 1", hits.Load())
	}
	m.mu.Lock()
	rt := m.runtimes[created.ID]
	delete(m.runtimes, created.ID)
	m.mu.Unlock()
	current()
	if hits.Load() != 1 {
		t.Fatalf("removed runtime job sent %d requests", hits.Load())
	}
	m.mu.Lock()
	m.runtimes[created.ID] = rt
	m.mu.Unlock()
}

func TestManagerResolveSeparatesStores(t *testing.T) {
	m := newTestManager(t, t.TempDir(), "")
	a, _ := m.Create(Settings{Name: "a", URL: "https://example.com/a"})
	b, _ := m.Create(Settings{Name: "b", URL: "https://example.com/b"})
	ra, _, _ := m.Resolve(a.ID)
	rb, _, _ := m.Resolve(b.ID)
	name := "same.png"
	if err := ra.Store.Import(bytes.NewReader(managerPNG(t, color.White)), name); err != nil {
		t.Fatal(err)
	}
	if err := rb.Store.Import(bytes.NewReader(managerPNG(t, color.Black)), name); err != nil {
		t.Fatal(err)
	}
	if _, ok := ra.Store.Get(name); !ok {
		t.Fatal("first board lost its image")
	}
	if _, ok := rb.Store.Get(name); !ok {
		t.Fatal("second board lost its image")
	}
	if _, _, err := m.Resolve("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve missing = %v", err)
	}
	archived := Board{
		ID:        "b-0123456789abcdef",
		Settings:  Settings{Name: "archived", URL: "https://example.com/archive"},
		Lifecycle: Archived,
		Created:   time.Now().UTC(),
	}
	if err := m.registry.Update(func(st *State) error {
		st.Boards = append(st.Boards, archived)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, ok := m.Board(archived.ID); !ok || got.Lifecycle != Archived {
		t.Fatalf("Board archived = %+v, %v", got, ok)
	}
	if _, _, err := m.Resolve(archived.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve archived = %v", err)
	}
	if all := m.All(); len(all) != 3 {
		t.Fatalf("All = %+v", all)
	}
}

func TestManagerListAndSuggested(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	o := managerOptions(t.TempDir(), "")
	o.Now = func() time.Time { return now }
	m, err := NewManager(o)
	if err != nil {
		t.Fatal(err)
	}
	older, _ := m.Create(Settings{Name: "older", URL: "https://example.com/old", Sort: -10})
	newer, _ := m.Create(Settings{Name: "newer", URL: "https://example.com/new", Hidden: true})
	emptyA, _ := m.Create(Settings{Name: "empty-a", URL: "https://example.com/a", Sort: 2})
	emptyB, _ := m.Create(Settings{Name: "empty-b", URL: "https://example.com/b", Sort: 1})
	rOld, _, _ := m.Resolve(older.ID)
	rNew, _, _ := m.Resolve(newer.ID)
	if err := rOld.Store.Import(bytes.NewReader(managerPNG(t, color.White)), "20261101-120000_00000000000000000000000000000001.png"); err != nil {
		t.Fatal(err)
	}
	if err := rNew.Store.Import(bytes.NewReader(managerPNG(t, color.Black)), "20261102-120000_00000000000000000000000000000002.png"); err != nil {
		t.Fatal(err)
	}
	visible := m.List(false)
	got := make([]string, len(visible))
	for i := range visible {
		got[i] = visible[i].ID
	}
	want := []string{older.ID, emptyB.ID, emptyA.ID}
	if !slices.Equal(got, want) || m.Suggested(false) != older.ID {
		t.Fatalf("visible order=%v suggested=%q want=%v", got, m.Suggested(false), want)
	}
	if all := m.List(true); len(all) != 4 || all[0].ID != newer.ID || m.Suggested(true) != newer.ID {
		t.Fatalf("all summaries=%+v", all)
	}
}

func TestManagerSummary(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	o := managerOptions(t.TempDir(), "")
	o.Now = func() time.Time { return now }
	m, err := NewManager(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	created, err := m.Create(Settings{Name: "hidden", URL: "https://example.com", Hidden: true, Schedule: "@every 2h"})
	if err != nil {
		t.Fatal(err)
	}
	rt, _, _ := m.Resolve(created.ID)
	body := managerPNG(t, color.White)
	if err := rt.Store.Import(bytes.NewReader(body), "20261101-120000_00000000000000000000000000000001.png"); err != nil {
		t.Fatal(err)
	}
	m.Start(context.Background())
	got, err := m.Summary(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	latest, _ := rt.Store.Latest()
	if got.Board != created.Board || got.Count != 1 || got.Bytes != int64(len(body)) ||
		!got.Latest.Equal(latest.Time) || !got.Paused || got.State != "paused" ||
		got.Schedule != "@every 2h" || got.NextRun.IsZero() {
		t.Fatalf("Summary = %+v", got)
	}
	if _, err := m.Summary("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Summary missing = %v", err)
	}
	for _, lifecycle := range []Lifecycle{Archiving, Archived, Deleting} {
		if err := m.registry.Update(func(st *State) error {
			st.Boards[0].Lifecycle = lifecycle
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if got, err := m.Summary(created.ID); !errors.Is(err, ErrNotFound) || got != (Summary{}) {
			t.Fatalf("Summary %s = %+v, %v", lifecycle, got, err)
		}
	}
	legacy := newTestManager(t, t.TempDir(), "https://example.com/legacy")
	alias, err := legacy.Summary("")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := legacy.Summary(LegacyID)
	if err != nil || alias != explicit || alias.ID != LegacyID {
		t.Fatalf("legacy Summary = %+v, explicit = %+v, error = %v", alias, explicit, err)
	}
}

func TestManagerWindowAndSharedCookie(t *testing.T) {
	var hits atomic.Int32
	body := managerPNG(t, color.White)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "down", http.StatusBadGateway)
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	o := managerOptions(t.TempDir(), "")
	now := time.Now().UTC()
	o.Now = func() time.Time { return now }
	m, err := NewManager(o)
	if err != nil {
		t.Fatal(err)
	}
	created, err := m.Create(Settings{Name: "future", URL: srv.URL, SyncFrom: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetPaused(created.ID, false); err != nil {
		t.Fatal(err)
	}
	rt, _, _ := m.Resolve(created.ID)
	other, err := m.Create(Settings{Name: "other", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	otherRT, _, _ := m.Resolve(other.ID)
	rt.Fetcher.RunScheduled(context.Background())
	if hits.Load() != 0 {
		t.Fatal("scheduled run ignored SyncFrom")
	}
	if _, err := rt.Fetcher.Run(context.Background()); err != nil || hits.Load() != 1 {
		t.Fatalf("manual Run = %v, hits=%d", err, hits.Load())
	}
	for range 2 {
		_, _ = rt.Fetcher.Run(context.Background())
	}
	for range 3 {
		_, _ = otherRT.Fetcher.Run(context.Background())
	}
	if rt.Fetcher.Status().RetryAt.IsZero() || otherRT.Fetcher.Status().RetryAt.IsZero() {
		t.Fatal("test did not establish a backoff")
	}
	if err := m.SetSharedCookie("sid=fresh"); err != nil {
		t.Fatal(err)
	}
	if !rt.Fetcher.Status().RetryAt.IsZero() || !otherRT.Fetcher.Status().RetryAt.IsZero() {
		t.Fatalf("shared cookie did not reset all backoffs: %+v / %+v", rt.Fetcher.Status(), otherRT.Fetcher.Status())
	}
	for range 3 {
		_, _ = rt.Fetcher.Run(context.Background())
		_, _ = otherRT.Fetcher.Run(context.Background())
	}
	if rt.Fetcher.Status().RetryAt.IsZero() || otherRT.Fetcher.Status().RetryAt.IsZero() {
		t.Fatal("test did not re-establish backoffs")
	}
	if err := m.ClearSharedCookie(); err != nil {
		t.Fatal(err)
	}
	if !rt.Fetcher.Status().RetryAt.IsZero() || !otherRT.Fetcher.Status().RetryAt.IsZero() {
		t.Fatalf("clearing cookie did not reset all backoffs: %+v / %+v", rt.Fetcher.Status(), otherRT.Fetcher.Status())
	}
}

func TestManagerStartStopEndsScheduledWork(t *testing.T) {
	var hits atomic.Int32
	body := managerPNG(t, color.White)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	o := managerOptions(t.TempDir(), "")
	m, err := NewManager(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	created, _ := m.Create(Settings{Name: "running", URL: srv.URL})
	if err := m.SetPaused(created.ID, false); err != nil {
		t.Fatal(err)
	}
	m.Start(context.Background())
	waitFor(t, func() bool { return hits.Load() > 0 })
	m.mu.RLock()
	job := m.job(created.ID, m.runtimes[created.ID].version)
	m.mu.RUnlock()
	done := make(chan struct{})
	go func() {
		m.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return")
	}
	after := hits.Load()
	job()
	if hits.Load() != after {
		t.Fatalf("requests continued after Stop: %d -> %d", after, hits.Load())
	}
}

func TestManagerStopCancelsBlockedRequests(t *testing.T) {
	for _, scheduled := range []bool{false, true} {
		name := "startup"
		if scheduled {
			name = "cron"
		}
		t.Run(name, func(t *testing.T) {
			entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				close(entered)
				select {
				case <-r.Context().Done():
					close(canceled)
				case <-release:
				}
			}))
			defer srv.Close()
			defer close(release)
			o := managerOptions(t.TempDir(), "")
			o.Magma.Timeout = 30 * time.Second
			if scheduled {
				o.Schedule = "@every 1s"
			}
			m, err := NewManager(o)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(m.Stop)
			created, err := m.Create(Settings{Name: "blocked", URL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			if scheduled {
				m.Start(context.Background())
				m.startup.Wait()
			}
			if err := m.SetPaused(created.ID, false); err != nil {
				t.Fatal(err)
			}
			if !scheduled {
				m.Start(context.Background())
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("fetch did not start")
			}
			done := make(chan struct{})
			go func() {
				m.Stop()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				// Let a regressed Stop finish before the test removes its files.
				release <- struct{}{}
				<-done
				t.Fatal("Stop did not cancel the blocked request")
			}
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("server request context was not canceled")
			}
			rt, _, _ := m.Resolve(created.ID)
			if got := rt.Fetcher.Status(); got.Runs != 0 || got.Failures != 0 {
				t.Fatalf("canceled fetch was recorded: %+v", got)
			}
			if len(rt.Fetcher.History()) != 0 || rt.Store.Count() != 0 {
				t.Fatal("canceled fetch wrote results")
			}
		})
	}
}

func TestManagerRequiresActiveBoardDirectory(t *testing.T) {
	root := t.TempDir()
	if _, err := Create(RegistryPath(root), State{
		Version: Version,
		Boards: []Board{{
			ID:        "b-0123456789abcdef",
			Settings:  Settings{Name: "missing", URL: "https://example.com"},
			Lifecycle: Active,
			Created:   time.Now().UTC(),
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager(managerOptions(root, "")); err == nil {
		t.Fatal("NewManager accepted a missing active board directory")
	}
}

func TestManagerDoesNotCreateRuntimeForInactiveBoard(t *testing.T) {
	root := t.TempDir()
	id := "b-0123456789abcdef"
	if _, err := Create(RegistryPath(root), State{
		Version: Version,
		Boards: []Board{{
			ID:        id,
			Settings:  Settings{Name: "archived", URL: "https://example.com"},
			Lifecycle: Archived,
			Created:   time.Now().UTC(),
		}},
	}); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(managerOptions(root, ""))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.runtimes) != 0 {
		t.Fatalf("inactive runtimes = %+v", m.runtimes)
	}
	if _, err := os.Stat(BoardDir(root, id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inactive board directory was created: %v", err)
	}
	if _, _, err := m.Resolve(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve inactive = %v", err)
	}
}

func TestManagerAlertsUseCurrentBoardName(t *testing.T) {
	root := t.TempDir()
	var event, message string
	o := managerOptions(root, "")
	o.Alert = func(gotEvent, gotMessage string) {
		event, message = gotEvent, gotMessage
	}
	m, err := NewManager(o)
	if err != nil {
		t.Fatal(err)
	}
	created, err := m.Create(Settings{Name: "before", URL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	rt, _, _ := m.Resolve(created.ID)
	if _, err := m.Update(created.ID, Settings{Name: "after", URL: "https://example.com"}); err != nil {
		t.Fatal(err)
	}
	rt.Fetcher.Alert("event", "message")
	if event != "event" || message != "[after] message" {
		t.Fatalf("alert = %q, %q", event, message)
	}

	nilAlert := newTestManager(t, t.TempDir(), "")
	nilBoard, err := nilAlert.Create(Settings{Name: "quiet", URL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	nilRT, _, _ := nilAlert.Resolve(nilBoard.ID)
	if nilRT.Fetcher.Alert != nil {
		t.Fatal("nil Manager alert installed a Fetcher callback")
	}
}

func TestManagerNilSharedDoesNotUseMagmaCookie(t *testing.T) {
	body := managerPNG(t, color.White)
	seen := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Cookie")
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	root := t.TempDir()
	o := managerOptions(root, "")
	o.Shared = nil
	o.Magma.Cookie = "sid=ignored"
	m, err := NewManager(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	created, err := m.Create(Settings{Name: "cookie", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	rt, _, _ := m.Resolve(created.ID)
	if got, err := rt.Fetcher.Run(context.Background()); err != nil || got.Result != fetcher.ResultSaved {
		t.Fatalf("first Run = %+v, %v", got, err)
	}
	if got := <-seen; got != "" {
		t.Fatalf("Magma.Cookie leaked to board: %q", got)
	}
	if err := m.SetSharedCookie("sid=shared"); err != nil {
		t.Fatal(err)
	}
	if got, err := rt.Fetcher.Run(context.Background()); err != nil || got.Result == fetcher.ResultError {
		t.Fatalf("second Run = %+v, %v", got, err)
	}
	if got := <-seen; got != "sid=shared" {
		t.Fatalf("shared cookie = %q", got)
	}
}

func TestManagerCreateAfterStartUsesChildContext(t *testing.T) {
	var hits atomic.Int32
	body := managerPNG(t, color.White)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	o := managerOptions(t.TempDir(), "")
	o.Alert = func(string, string) {}
	m, err := NewManager(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	m.Start(context.Background())
	created, err := m.Create(Settings{Name: "late", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if !created.Paused {
		t.Fatalf("late board was not paused: %+v", created)
	}
	m.startup.Wait()
	m.mu.RLock()
	rt := m.runtimes[created.ID]
	version := rt.version
	m.mu.RUnlock()
	if rt.ctx == nil || rt.ctx.Err() != nil {
		t.Fatalf("late board context = %v", rt.ctx)
	}
	if err := m.SetPaused(created.ID, false); err != nil {
		t.Fatal(err)
	}
	job := m.job(created.ID, version)
	job()
	if hits.Load() != 1 {
		t.Fatalf("late board job sent %d requests", hits.Load())
	}
	m.Stop()
	if !errors.Is(rt.ctx.Err(), context.Canceled) {
		t.Fatalf("late board context after Stop = %v", rt.ctx.Err())
	}
	job()
	if hits.Load() != 1 {
		t.Fatalf("late board job ran after Stop: %d", hits.Load())
	}
}
