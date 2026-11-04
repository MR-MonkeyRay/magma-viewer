package board

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
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

func managerOptions(root string) Options {
	return Options{
		Root:     root,
		TempDir:  root,
		Magma:    config.Magma{Timeout: 2 * time.Second},
		Schedule: "@every 1h",
		Shared:   fetcher.NewCredentials(filepath.Join(root, ".state", "cookie.json")),
	}
}

func newTestManager(t *testing.T, root string) *Manager {
	t.Helper()
	m, err := NewManager(managerOptions(root))
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
	m := newTestManager(t, root)
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
	m := newTestManager(t, root)
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

	reloaded := newTestManager(t, root)
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
	m := newTestManager(t, root)
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

	m := newTestManager(t, t.TempDir())
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
	if updated.Name != "two" {
		t.Fatalf("updated = %+v", updated)
	}
	overridden := m.NextRun(created.ID)
	if before.IsZero() || overridden.IsZero() || before.Equal(overridden) {
		t.Fatalf("NextRun did not change: %v -> %v", before, overridden)
	}
	if _, err := m.Update(created.ID, Settings{Name: "two", URL: b.URL}); err != nil {
		t.Fatal(err)
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
	m := newTestManager(t, t.TempDir())
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
	m := newTestManager(t, root)
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
	m := newTestManager(t, t.TempDir())
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
	m := newTestManager(t, t.TempDir())
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
	o := managerOptions(t.TempDir())
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
	o := managerOptions(t.TempDir())
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
	legacyRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(legacyRoot, "legacy.png"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	legacy := newTestManager(t, legacyRoot)
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
	o := managerOptions(t.TempDir())
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
	o := managerOptions(t.TempDir())
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
			o := managerOptions(t.TempDir())
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
	if _, err := NewManager(managerOptions(root)); err == nil {
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
	m, err := NewManager(managerOptions(root))
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
	o := managerOptions(root)
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

	nilAlert := newTestManager(t, t.TempDir())
	nilBoard, err := nilAlert.Create(Settings{Name: "quiet", URL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	nilRT, _, _ := nilAlert.Resolve(nilBoard.ID)
	if nilRT.Fetcher.Alert != nil {
		t.Fatal("nil Manager alert installed a Fetcher callback")
	}
}

func TestManagerNilSharedUsesOnlineCookie(t *testing.T) {
	body := managerPNG(t, color.White)
	seen := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Cookie")
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	root := t.TempDir()
	o := managerOptions(root)
	o.Shared = nil
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
		t.Fatalf("unexpected cookie = %q", got)
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
	o := managerOptions(t.TempDir())
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

func maintenanceBoard(t *testing.T, m *Manager, name string) (*Runtime, Board) {
	t.Helper()
	s, err := m.Create(Settings{Name: name, URL: "https://example.com/" + name})
	if err != nil {
		t.Fatal(err)
	}
	rt, b, err := m.Resolve(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"one.png": managerPNG(t, color.White), "two.png": managerPNG(t, color.Black)} {
		if err := rt.Store.Import(bytes.NewReader(data), name); err != nil {
			t.Fatal(err)
		}
	}
	return rt, b
}

func awaitOperation(t *testing.T, m *Manager, id string) Operation {
	t.Helper()
	waitFor(t, func() bool {
		op, ok := m.Operation(id)
		m.mu.RLock()
		busy := m.busy
		m.mu.RUnlock()
		return ok && !op.Finished.IsZero() && !busy
	})
	op, _ := m.Operation(id)
	return op
}

func TestManagerArchive(t *testing.T) {
	root := t.TempDir()
	m := newTestManager(t, root)
	rt, b := maintenanceBoard(t, m, "archive")
	const cookie = "sid=private-board-cookie"
	if err := rt.Fetcher.SetCookie(cookie); err != nil {
		t.Fatal(err)
	}
	sources := make(map[string][]byte)
	for _, item := range rt.Store.All() {
		data, err := os.ReadFile(filepath.Join(rt.Store.Dir(), item.Name))
		if err != nil {
			t.Fatal(err)
		}
		sources[item.Name] = data
	}
	m.Start(context.Background())
	if _, ok := m.Operation(b.ID); ok {
		t.Fatal("unexpected operation")
	}
	if err := m.Archive(b.ID); err != nil {
		t.Fatal(err)
	}
	op := awaitOperation(t, m, b.ID)
	if op.Err != "" || op.Kind != "archive" || op.Done != 2 || op.Total != 2 || op.Started.IsZero() {
		t.Fatalf("operation = %+v", op)
	}
	archives := m.Archives()
	if len(archives) != 1 || archives[0].BoardID != b.ID || archives[0].BoardName != b.Name || archives[0].Images != 2 {
		t.Fatalf("archives = %+v", archives)
	}
	path, a, err := m.ArchiveFile(archives[0].ID)
	if err != nil || a != archives[0] || filepath.Dir(path) != filepath.Join(root, "archives") {
		t.Fatalf("ArchiveFile = %q, %+v, %v", path, a, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != a.Size {
		t.Fatalf("archive stat = %v, %v", info, err)
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	if len(zr.File) != len(sources)+1 {
		t.Fatalf("entries = %d", len(zr.File))
	}
	for _, zf := range zr.File {
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(cookie)) {
			t.Fatalf("cookie leaked into %s", zf.Name)
		}
		if zf.Name == manifestName {
			var manifest archiveManifest
			if err := json.Unmarshal(data, &manifest); err != nil {
				t.Fatal(err)
			}
			if manifest.BoardID != b.ID || manifest.Name != b.Name || manifest.Images != len(sources) || !manifest.Created.Equal(a.Created) {
				t.Fatalf("manifest = %+v", manifest)
			}
		} else if !bytes.Equal(data, sources[zf.Name]) {
			t.Fatalf("source differs: %s", zf.Name)
		}
	}
	assertMigrationAbsent(t, BoardDir(root, b.ID))
	if _, _, err := m.Resolve(b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve = %v", err)
	}
	if len(m.List(true)) != 0 || len(m.All()) != 1 {
		t.Fatalf("list/all = %+v/%+v", m.List(true), m.All())
	}
	registry, err := Load(RegistryPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := registry.Board(b.ID); got.Lifecycle != Archived {
		t.Fatalf("persisted = %+v", got)
	}
}

func TestManagerArchiveFailureRestoresRuntimeAndPause(t *testing.T) {
	for _, paused := range []bool{false, true} {
		t.Run(fmt.Sprint(paused), func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.Write(managerPNG(t, color.White))
			}))
			defer srv.Close()
			root := t.TempDir()
			m := newTestManager(t, root)
			rt, b := maintenanceBoard(t, m, "failure")
			if _, err := m.Update(b.ID, Settings{Name: b.Name, URL: srv.URL}); err != nil {
				t.Fatal(err)
			}
			if err := m.SetPaused(b.ID, paused); err != nil {
				t.Fatal(err)
			}
			m.Start(context.Background())
			m.startup.Wait()
			// A broken image link is indexed but cannot be opened, even as root.
			if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(rt.Store.Dir(), "broken.png")); err != nil {
				t.Fatal(err)
			}
			m.mu.RLock()
			stale := m.job(b.ID, rt.version)
			m.mu.RUnlock()
			if err := m.Archive(b.ID); err != nil {
				t.Fatal(err)
			}
			op := awaitOperation(t, m, b.ID)
			if op.Err == "" {
				t.Fatal("archive did not fail")
			}
			restored, current, err := m.Resolve(b.ID)
			if err != nil || current.Lifecycle != Active || restored == rt || restored.Fetcher.Status().Paused != paused {
				t.Fatalf("restored = %+v, err=%v, paused=%v", current, err, restored.Fetcher.Status().Paused)
			}
			for _, name := range []string{"one.png", "two.png"} {
				if _, err := os.Stat(filepath.Join(restored.Store.Dir(), name)); err != nil {
					t.Fatal(err)
				}
			}
			entries, err := os.ReadDir(filepath.Join(root, "archives"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("archive leftovers = %v, %v", entries, err)
			}
			m.startup.Wait()
			before := hits.Load()
			stale()
			if hits.Load() != before {
				t.Fatal("retired cron version became valid again")
			}
			m.mu.RLock()
			job := m.job(b.ID, restored.version)
			entry := restored.entry
			m.mu.RUnlock()
			if _, err := rt.Fetcher.Run(context.Background()); !errors.Is(err, fetcher.ErrInactive) {
				t.Fatalf("retired fetcher reactivated: %v", err)
			}
			if entry == 0 || m.NextRun(b.ID).IsZero() {
				t.Fatal("schedule was not restored")
			}
			job()
			if want := before + 1; !paused && hits.Load() != want {
				t.Fatalf("restored cron hits=%d, want=%d", hits.Load(), want)
			}
			if paused && hits.Load() != before {
				t.Fatal("pause state was lost")
			}
		})
	}
}

func TestManagerMaintenanceExclusionAndInactiveGates(t *testing.T) {
	m := newTestManager(t, t.TempDir())
	rt, b := maintenanceBoard(t, m, "held")
	_, other := maintenanceBoard(t, m, "other")
	a := registeredArchive(t, m, b.ID, "old.zip")
	m.Start(context.Background())
	m.startup.Wait()
	m.mu.Lock()
	rt.wg.Add(1)
	stale := m.job(b.ID, rt.version)
	m.mu.Unlock()
	defer rt.wg.Done()
	if err := m.Archive(b.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Archive(other.ID); !errors.Is(err, ErrMaintenanceBusy) {
		t.Fatalf("Archive = %v", err)
	}
	if err := m.Delete(other.ID, other.Name); !errors.Is(err, ErrMaintenanceBusy) {
		t.Fatalf("Delete = %v", err)
	}
	if _, _, err := m.ArchiveFile(a.ID); !errors.Is(err, ErrMaintenanceBusy) {
		t.Fatalf("ArchiveFile = %v", err)
	}
	if err := m.DeleteArchive(a.ID); !errors.Is(err, ErrMaintenanceBusy) {
		t.Fatalf("DeleteArchive = %v", err)
	}
	if _, err := rt.Fetcher.Run(context.Background()); !errors.Is(err, fetcher.ErrInactive) {
		t.Fatalf("Run = %v", err)
	}
	if _, err := m.Update(b.ID, b.Settings); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update = %v", err)
	}
	if err := m.SetPaused(b.ID, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetPaused = %v", err)
	}
	before := rt.Fetcher.Status().Runs
	stale()
	rt.Fetcher.RunScheduled(context.Background())
	if rt.Fetcher.Status().Runs != before {
		t.Fatal("inactive cron fetched")
	}
}

func registeredArchive(t *testing.T, m *Manager, id, file string) Archive {
	t.Helper()
	aid, err := NewArchiveID()
	if err != nil {
		t.Fatal(err)
	}
	a := Archive{ID: aid, BoardID: id, File: file, Created: time.Now().UTC()}
	writeMigrationFile(t, filepath.Join(m.o.Root, "archives", file), "archive data")
	if err := m.registry.Update(func(st *State) error { st.Archives = append(st.Archives, a); return nil }); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestManagerDelete(t *testing.T) {
	for _, archived := range []bool{false, true} {
		t.Run(fmt.Sprint(archived), func(t *testing.T) {
			root := t.TempDir()
			m := newTestManager(t, root)
			_, b := maintenanceBoard(t, m, "target")
			otherRT, other := maintenanceBoard(t, m, "other")
			kept := registeredArchive(t, m, other.ID, "keep.zip")
			registeredArchive(t, m, b.ID, "target-old.zip")
			orphan := filepath.Join(root, "archives", b.ID+"--orphan.zip")
			part := filepath.Join(root, "archives", "."+b.ID+"--unfinished.zip.part")
			writeMigrationFile(t, orphan, "orphan")
			writeMigrationFile(t, part, "part")
			m.Start(context.Background())
			if archived {
				if err := m.Archive(b.ID); err != nil {
					t.Fatal(err)
				}
				if op := awaitOperation(t, m, b.ID); op.Err != "" {
					t.Fatalf("archive: %+v", op)
				}
			}
			before, err := os.ReadFile(RegistryPath(root))
			if err != nil {
				t.Fatal(err)
			}
			err = m.Delete(b.ID, "wrong")
			var invalid InvalidError
			if !errors.As(err, &invalid) || invalid.Code != "confirm_mismatch" {
				t.Fatalf("confirm = %v", err)
			}
			after, err := os.ReadFile(RegistryPath(root))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("bad confirmation changed registry")
			}
			if err := m.Delete(b.ID, "  "+b.Name+" \n"); err != nil {
				t.Fatal(err)
			}
			if op := awaitOperation(t, m, b.ID); op.Err != "" || op.Kind != "delete" {
				t.Fatalf("delete: %+v", op)
			}
			assertMigrationAbsent(t, BoardDir(root, b.ID))
			assertMigrationAbsent(t, orphan)
			assertMigrationAbsent(t, part)
			assertMigrationAbsent(t, filepath.Join(root, "archives", "target-old.zip"))
			if _, ok := m.Board(b.ID); ok {
				t.Fatal("deleted board remains")
			}
			if len(m.Archives()) != 1 || m.Archives()[0].ID != kept.ID {
				t.Fatalf("archives=%+v", m.Archives())
			}
			if _, _, err := m.Resolve(other.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(otherRT.Store.Dir(), "one.png")); err != nil {
				t.Fatal(err)
			}
			registry, err := Load(RegistryPath(root))
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := registry.Board(b.ID); ok {
				t.Fatal("deleted board persisted")
			}
		})
	}
}

func TestManagerDeleteArchive(t *testing.T) {
	root := t.TempDir()
	m := newTestManager(t, root)
	_, b := maintenanceBoard(t, m, "archived")
	if err := m.Archive(b.ID); err != nil {
		t.Fatal(err)
	}
	if op := awaitOperation(t, m, b.ID); op.Err != "" {
		t.Fatal(op.Err)
	}
	first := m.Archives()[0]
	second := registeredArchive(t, m, b.ID, "second.zip")
	if got := m.Archives(); len(got) != 2 || got[0].ID != second.ID {
		t.Fatalf("archives order = %+v", got)
	}
	if err := m.DeleteArchive(first.ID); err != nil {
		t.Fatal(err)
	}
	assertMigrationAbsent(t, filepath.Join(root, "archives", first.File))
	if current, ok := m.Board(b.ID); !ok || current.Lifecycle != Archived {
		t.Fatal("board removed before last archive")
	}
	if err := os.Remove(filepath.Join(root, "archives", second.File)); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteArchive(second.ID); err != nil {
		t.Fatal(err)
	}
	if len(m.Archives()) != 0 {
		t.Fatal("archive records remain")
	}
	if _, ok := m.Board(b.ID); ok {
		t.Fatal("last archive left board record")
	}
	if err := m.DeleteArchive(second.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteArchive missing = %v", err)
	}
	if _, _, err := m.ArchiveFile(second.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ArchiveFile missing = %v", err)
	}
}

func TestManagerRecoveryRunsSerially(t *testing.T) {
	root := t.TempDir()
	original := newTestManager(t, root)
	rt, archiving := maintenanceBoard(t, original, "archiving")
	_, archived := maintenanceBoard(t, original, "archived")
	_, deleting := maintenanceBoard(t, original, "deleting")
	registered := registeredArchive(t, original, archiving.ID, archiving.ID+"--20000101-000000.zip")
	removed := registeredArchive(t, original, deleting.ID, "delete.zip")
	part := filepath.Join(root, "archives", "."+archiving.ID+"--unfinished.zip.part")
	orphan := filepath.Join(root, "archives", archiving.ID+"--orphan.zip")
	writeMigrationFile(t, part, "part")
	writeMigrationFile(t, orphan, "orphan")
	if err := original.SetPaused(archiving.ID, false); err != nil {
		t.Fatal(err)
	}
	original.Stop()
	if err := original.registry.Update(func(st *State) error {
		st.Boards[0].Lifecycle = Archiving
		st.Boards[1].Lifecycle = Archived
		st.Boards[2].Lifecycle = Deleting
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m := newTestManager(t, root)
	if len(m.runtimes) != 1 || m.runtimes[archiving.ID].entry != 0 {
		t.Fatalf("recovery runtimes = %+v", m.runtimes)
	}
	m.mu.Lock()
	recoveryRT := m.runtimes[archiving.ID]
	recoveryRT.wg.Add(1)
	var release sync.Once
	defer release.Do(recoveryRT.wg.Done)
	m.mu.Unlock()
	m.Start(context.Background())
	if err := m.Archive(archiving.ID); !errors.Is(err, ErrMaintenanceBusy) {
		t.Fatalf("recovery exclusion = %v", err)
	}
	if _, err := os.Stat(BoardDir(root, deleting.ID)); err != nil {
		t.Fatal("recovery ran out of order")
	}
	release.Do(recoveryRT.wg.Done)
	op1 := awaitOperation(t, m, archiving.ID)
	if recoveryRT.Fetcher.Status().Runs != 0 {
		t.Fatal("recovery started a fetch")
	}
	op2, ok2 := m.Operation(archived.ID)
	op3, ok3 := m.Operation(deleting.ID)
	if op1.Err != "" || !ok2 || op2.Err != "" || !ok3 || op3.Err != "" || op2.Started.Before(op1.Finished) || op3.Started.Before(op2.Finished) {
		t.Fatalf("operations = %+v %+v %+v", op1, op2, op3)
	}
	for _, b := range []Board{archiving, archived, deleting} {
		assertMigrationAbsent(t, BoardDir(root, b.ID))
	}
	assertMigrationAbsent(t, part)
	assertMigrationAbsent(t, orphan)
	assertMigrationAbsent(t, filepath.Join(root, "archives", removed.File))
	assertMigrationFile(t, filepath.Join(root, "archives", registered.File), "archive data")
	if current, ok := m.Board(archiving.ID); !ok || current.Lifecycle != Archived {
		t.Fatalf("recovered archive=%+v", current)
	}
	if _, ok := m.Board(deleting.ID); ok {
		t.Fatal("recovering delete retained record")
	}
	if len(m.Archives()) != 2 || rt.Store.Count() != 2 {
		t.Fatalf("archives=%+v", m.Archives())
	}
}

func TestManagerStopDuringArchiveLeavesRecoverableSources(t *testing.T) {
	root := t.TempDir()
	m := newTestManager(t, root)
	rt, b := maintenanceBoard(t, m, "cancel")
	m.Start(context.Background())
	m.startup.Wait()
	rt.wg.Add(1)
	if err := m.Archive(b.ID); err != nil {
		rt.wg.Done()
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() { m.Stop(); close(stopped) }()
	<-m.ctx.Done()
	rt.wg.Done()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not finish")
	}
	op, _ := m.Operation(b.ID)
	if op.Err == "" {
		t.Fatalf("cancelled operation=%+v", op)
	}
	registry, err := Load(RegistryPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if current, _ := registry.Board(b.ID); current.Lifecycle != Archiving {
		t.Fatalf("cancelled lifecycle=%s", current.Lifecycle)
	}
	for _, name := range []string{"one.png", "two.png"} {
		if _, err := os.Stat(filepath.Join(rt.Store.Dir(), name)); err != nil {
			t.Fatal(err)
		}
	}
	files, err := filepath.Glob(filepath.Join(root, "archives", "*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("cancel leftovers=%v, %v", files, err)
	}
	recovered := newTestManager(t, root)
	recovered.Start(context.Background())
	if op := awaitOperation(t, recovered, b.ID); op.Err != "" {
		t.Fatal(op.Err)
	}
	assertMigrationAbsent(t, BoardDir(root, b.ID))
}

func TestManagerRetireDoesNotWaitForOtherBoards(t *testing.T) {
	m := newTestManager(t, t.TempDir())
	_, target := maintenanceBoard(t, m, "target")
	other, _ := maintenanceBoard(t, m, "other")
	m.Start(context.Background())
	other.wg.Add(1)
	defer other.wg.Done()
	if err := m.Archive(target.ID); err != nil {
		t.Fatal(err)
	}
	if op := awaitOperation(t, m, target.ID); op.Err != "" {
		t.Fatal(op.Err)
	}
}

func TestManagerDeleteFailureResumesOnRestart(t *testing.T) {
	root := t.TempDir()
	m := newTestManager(t, root)
	_, b := maintenanceBoard(t, m, "delete")
	a := registeredArchive(t, m, b.ID, "blocked.zip")
	path := filepath.Join(root, "archives", a.File)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writeMigrationFile(t, filepath.Join(path, "blocker"), "cannot remove nonempty directory as a file")
	m.Start(context.Background())
	if err := m.Delete(b.ID, b.Name); err != nil {
		t.Fatal(err)
	}
	op := awaitOperation(t, m, b.ID)
	if op.Err == "" {
		t.Fatal("delete did not fail")
	}
	if current, _ := m.Board(b.ID); current.Lifecycle != Deleting {
		t.Fatalf("failed deletion = %+v", current)
	}
	if _, err := m.Update(b.ID, b.Settings); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update deleting = %v", err)
	}
	m.Stop()
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	recovered := newTestManager(t, root)
	recovered.Start(context.Background())
	if op := awaitOperation(t, recovered, b.ID); op.Err != "" {
		t.Fatal(op.Err)
	}
	if _, ok := recovered.Board(b.ID); ok {
		t.Fatal("resumed deletion left a board")
	}
	if len(recovered.Archives()) != 0 {
		t.Fatal("resumed deletion left archives")
	}
}

func TestManagerArchiveCommitFailureKeepsSourcesForRecovery(t *testing.T) {
	root := t.TempDir()
	m := newTestManager(t, root)
	rt, b := maintenanceBoard(t, m, "commit")
	m.Start(context.Background())
	m.startup.Wait()
	rt.wg.Add(1)
	if err := m.Archive(b.ID); err != nil {
		rt.wg.Done()
		t.Fatal(err)
	}
	originalPath := m.registry.path
	blocker := filepath.Join(root, "registry-blocker")
	writeMigrationFile(t, blocker, "blocker")
	m.registry.path = filepath.Join(blocker, "boards.json")
	rt.wg.Done()
	op := awaitOperation(t, m, b.ID)
	m.registry.path = originalPath
	if op.Err == "" {
		t.Fatal("registry commit did not fail")
	}
	if current, _ := m.Board(b.ID); current.Lifecycle != Archiving {
		t.Fatalf("failed commit changed lifecycle: %+v", current)
	}
	if len(m.Archives()) != 0 {
		t.Fatal("failed commit registered an archive")
	}
	entries, err := os.ReadDir(filepath.Join(root, "archives"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("commit failure leftovers = %v, %v", entries, err)
	}
	for _, name := range []string{"one.png", "two.png"} {
		if _, err := os.Stat(filepath.Join(rt.Store.Dir(), name)); err != nil {
			t.Fatal(err)
		}
	}
	m.Stop()
	recovered := newTestManager(t, root)
	recovered.Start(context.Background())
	if op := awaitOperation(t, recovered, b.ID); op.Err != "" {
		t.Fatal(op.Err)
	}
}

func TestManagerArchiveCleanupFailureResumesOnRestart(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("directory permissions do not restrict root")
	}
	root := t.TempDir()
	m := newTestManager(t, root)
	rt, b := maintenanceBoard(t, m, "cleanup")
	m.Start(context.Background())
	m.startup.Wait()
	rt.wg.Add(1)
	var release sync.Once
	defer release.Do(rt.wg.Done)
	if err := m.Archive(b.ID); err != nil {
		t.Fatal(err)
	}
	dir := BoardDir(root, b.ID)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	release.Do(rt.wg.Done)
	if op := awaitOperation(t, m, b.ID); op.Err == "" {
		t.Fatal("cleanup did not fail")
	}
	if current, _ := m.Board(b.ID); current.Lifecycle != Archived {
		t.Fatalf("cleanup failure rolled back = %+v", current)
	}
	archives := m.Archives()
	if len(archives) != 1 {
		t.Fatalf("committed archive = %+v", archives)
	}
	if _, _, err := m.ArchiveFile(archives[0].ID); err != nil {
		t.Fatal(err)
	}
	m.mu.RLock()
	rt = m.runtimes[b.ID]
	m.mu.RUnlock()
	if rt != nil {
		t.Fatal("archived runtime retained after cleanup failure")
	}
	m.Stop()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	recovered := newTestManager(t, root)
	recovered.Start(context.Background())
	if op := awaitOperation(t, recovered, b.ID); op.Err != "" {
		t.Fatal(op.Err)
	}
	assertMigrationAbsent(t, dir)
	if len(recovered.Archives()) != 1 {
		t.Fatal("cleanup lost committed archive")
	}
}

func TestManagerStartQueuesRecoveryBehindExistingTask(t *testing.T) {
	root := t.TempDir()
	original := newTestManager(t, root)
	_, target := maintenanceBoard(t, original, "target")
	_, pending := maintenanceBoard(t, original, "pending")
	original.Stop()
	if err := original.lifecycle(pending.ID, Deleting); err != nil {
		t.Fatal(err)
	}
	m := newTestManager(t, root)
	rt, _, err := m.Resolve(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	rt.wg.Add(1)
	var release sync.Once
	defer release.Do(rt.wg.Done)
	if err := m.Archive(target.ID); err != nil {
		t.Fatal(err)
	}
	m.Start(context.Background())
	release.Do(rt.wg.Done)
	if op := awaitOperation(t, m, target.ID); op.Err != "" {
		t.Fatal(op.Err)
	}
	if op, ok := m.Operation(pending.ID); !ok || op.Err != "" || op.Finished.IsZero() {
		t.Fatalf("pending recovery = %+v, %v", op, ok)
	}
	if _, ok := m.Board(pending.ID); ok {
		t.Fatal("pending deletion was skipped")
	}
}

func TestManagerStartSkipsCompletedArchives(t *testing.T) {
	root := t.TempDir()
	original := newTestManager(t, root)
	_, b := maintenanceBoard(t, original, "complete")
	if err := original.Archive(b.ID); err != nil {
		t.Fatal(err)
	}
	if op := awaitOperation(t, original, b.ID); op.Err != "" {
		t.Fatal(op.Err)
	}
	original.Stop()
	m := newTestManager(t, root)
	m.Start(context.Background())
	m.mu.RLock()
	busy, queued := m.busy, len(m.pending)
	m.mu.RUnlock()
	if busy || queued != 0 {
		t.Fatal("completed archive occupied maintenance slot")
	}
	if _, ok := m.Operation(b.ID); ok {
		t.Fatal("completed archive was queued again")
	}
}

func TestManagerRetiredRuntimeRejectsCookieMutations(t *testing.T) {
	for _, kind := range []string{"archive", "delete"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			m := newTestManager(t, root)
			rt, b := maintenanceBoard(t, m, "retire")
			if err := rt.Fetcher.SetCookie("sid=original"); err != nil {
				t.Fatal(err)
			}
			m.Start(context.Background())
			var err error
			if kind == "archive" {
				err = m.Archive(b.ID)
			} else {
				err = m.Delete(b.ID, b.Name)
			}
			if err != nil {
				t.Fatal(err)
			}
			if op := awaitOperation(t, m, b.ID); op.Err != "" {
				t.Fatal(op.Err)
			}
			dir := BoardDir(root, b.ID)
			assertMigrationAbsent(t, dir)
			if err := rt.Fetcher.SetCookie("sid=late-request"); !errors.Is(err, fetcher.ErrInactive) {
				t.Fatalf("SetCookie after %s = %v", kind, err)
			}
			assertMigrationAbsent(t, dir)
			if err := rt.Fetcher.ClearCookie(); !errors.Is(err, fetcher.ErrInactive) {
				t.Fatalf("ClearCookie after %s = %v", kind, err)
			}
			assertMigrationAbsent(t, dir)
		})
	}
}
