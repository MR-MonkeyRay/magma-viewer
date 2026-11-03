package fetcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/color"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"magma-viewer/internal/config"
	"magma-viewer/internal/store"
)

func TestCookieInheritance(t *testing.T) {
	root := t.TempDir()
	shared := NewCredentials("sid=config", filepath.Join(root, ".state", "cookie.json"))
	st, _ := store.New(filepath.Join(root, "boards", "a"))
	f := New(config.Magma{Cookie: "sid=ignored"}, t.TempDir(), st, shared)
	t.Cleanup(f.Drain)
	otherStore, _ := store.New(filepath.Join(root, "boards", "b"))
	other := New(config.Magma{}, t.TempDir(), otherStore, shared)
	t.Cleanup(other.Drain)
	check := func(f *Fetcher, want, source string, updated time.Time) {
		t.Helper()
		cookie, info := f.effectiveCookie()
		if cookie != want || info.Source != source || !info.Updated.Equal(updated) || f.Cookie() != info {
			t.Fatalf("Cookie = %q, %+v; want %q, %s, %v", cookie, info, want, source, updated)
		}
	}
	check(f, "sid=config", "config", time.Time{})
	pageTime := time.Date(2026, 11, 2, 12, 0, 0, 1, time.UTC)
	shared.now = func() time.Time { return pageTime }
	if err := shared.Set("sid=page"); err != nil {
		t.Fatal(err)
	}
	check(f, "sid=page", "page", pageTime)
	check(other, "sid=page", "page", pageTime)
	boardTime := pageTime.Add(time.Minute)
	f.now = func() time.Time { return boardTime }
	if err := f.SetCookie("sid=board"); err != nil {
		t.Fatal(err)
	}
	check(f, "sid=board", "board", boardTime)
	data, err := os.ReadFile(f.cookiePath())
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]json.RawMessage
	if err := json.Unmarshal(data, &saved); err != nil || len(saved) != 2 || saved["cookie"] == nil || saved["updated"] == nil {
		t.Fatalf("board cookie format = %s, %v", data, err)
	}
	pageTime = pageTime.Add(2 * time.Minute)
	if err := shared.Set("sid=new-page"); err != nil {
		t.Fatal(err)
	}
	check(f, "sid=board", "board", boardTime)
	check(other, "sid=new-page", "page", pageTime)
	if err := f.ClearCookie(); err != nil {
		t.Fatal(err)
	}
	check(f, "sid=new-page", "page", pageTime)
	if err := f.ClearCookie(); err != nil {
		t.Fatalf("second ClearCookie: %v", err)
	}
	if err := shared.Clear(); err != nil {
		t.Fatal(err)
	}
	check(f, "sid=config", "config", time.Time{})
}

func TestUnreadableBoardCookieIsIgnored(t *testing.T) {
	for _, data := range []string{"{", `{"cookie":""}`, `{"cookie":42}`} {
		t.Run(data, func(t *testing.T) {
			st, _ := store.New(t.TempDir())
			if err := os.WriteFile(filepath.Join(st.Dir(), cookieFile), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			f := newTestFetcher(t, config.Magma{Cookie: "sid=config"}, t.TempDir(), st)
			if cookie, info := f.effectiveCookie(); cookie != "sid=config" || info.Source != "config" {
				t.Fatalf("bad override used: %q, %+v", cookie, info)
			}
		})
	}
}

func TestCookieHeadersUseCredentials(t *testing.T) {
	img := pngBytes(t, color.White)
	for _, cookie := range []string{"sid=shared", ""} {
		t.Run(cookie, func(t *testing.T) {
			seen := make(chan http.Header, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen <- r.Header.Clone()
				w.Write(img)
			}))
			defer srv.Close()
			st, _ := store.New(t.TempDir())
			shared := NewCredentials(cookie, filepath.Join(st.Dir(), ".state", "cookie.json"))
			cfg := config.Magma{URL: srv.URL, Cookie: "sid=ignored-config", Headers: map[string]string{
				"Cookie": "sid=ignored-header", "cOoKiE": "sid=ignored-mixed", "X-Test": "kept",
			}}
			f := New(cfg, t.TempDir(), st, shared)
			t.Cleanup(f.Drain)
			if got, err := f.Run(context.Background()); err != nil || got.Result != ResultSaved {
				t.Fatalf("Run = %+v, %v", got, err)
			}
			if h := <-seen; h.Get("Cookie") != cookie || h.Get("X-Test") != "kept" {
				t.Fatalf("headers = %v", h)
			}
		})
	}
}

func TestValidatorsStayWithTheirCookie(t *testing.T) {
	img := pngBytes(t, color.White)
	seen := make(chan http.Header, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Mon, 02 Nov 2026 12:00:00 GMT")
		w.Write(img)
	}))
	defer srv.Close()
	st, _ := store.New(t.TempDir())
	f := newTestFetcher(t, config.Magma{URL: srv.URL, Cookie: "sid=config"}, t.TempDir(), st)
	steps := []struct {
		update      func() error
		conditional bool
	}{
		{nil, false},
		{nil, true},
		{func() error { return f.shared.Set("sid=page") }, false},
		{func() error { return f.SetCookie("sid=board") }, false},
		{func() error { return f.shared.Set("sid=new-page") }, true},
		{f.ClearCookie, false},
	}
	for i, step := range steps {
		if step.update != nil {
			if err := step.update(); err != nil {
				t.Fatal(err)
			}
		}
		if got, err := f.Run(context.Background()); err != nil || got.Result == ResultError {
			t.Fatalf("step %d: %+v, %v", i, got, err)
		}
		h := <-seen
		if (h.Get("If-None-Match") != "") != step.conditional || (h.Get("If-Modified-Since") != "") != step.conditional {
			t.Fatalf("step %d: validators = %v, conditional = %v", i, h, step.conditional)
		}
	}
}

func TestCookieSnapshotSurvivesSharedUpdateDuringFetch(t *testing.T) {
	img := pngBytes(t, color.White)
	entered, release := make(chan struct{}), make(chan struct{})
	var hits atomic.Int32
	seen := make(chan http.Header, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
		if hits.Add(1) == 1 {
			close(entered)
			<-release
		}
		w.Header().Set("ETag", `"v1"`)
		w.Write(img)
	}))
	defer srv.Close()
	st, _ := store.New(t.TempDir())
	f := newTestFetcher(t, config.Magma{URL: srv.URL, Cookie: "sid=old"}, t.TempDir(), st)
	done := make(chan struct{})
	go func() { f.Run(context.Background()); close(done) }()
	<-entered
	err := f.shared.Set("sid=new")
	close(release)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	f.Run(context.Background())
	first, second := <-seen, <-seen
	if first.Get("Cookie") != "sid=old" || second.Get("Cookie") != "sid=new" || second.Get("If-None-Match") != "" {
		t.Fatalf("requests = %v, %v", first, second)
	}
}

func TestRunGates(t *testing.T) {
	var hits atomic.Int32
	img := pngBytes(t, color.White)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write(img)
	}))
	defer srv.Close()
	for _, gate := range []string{"inactive", "window", "no_url"} {
		t.Run(gate, func(t *testing.T) {
			st, _ := store.New(t.TempDir())
			f := newTestFetcher(t, config.Magma{URL: srv.URL}, t.TempDir(), st)
			var want error
			switch gate {
			case "inactive":
				f.Active = func() bool { return false }
				want = ErrInactive
			case "window":
				clock := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
				f.now = func() time.Time { return clock }
				f.InWindow = func(now time.Time) bool {
					if now != clock {
						t.Errorf("InWindow time = %v, want %v", now, clock)
					}
					return false
				}
			case "no_url":
				f.URL = func() string { return "" }
				want = ErrNoURL
			}
			before := hits.Load()
			f.RunScheduled(context.Background())
			if hits.Load() != before || len(f.History()) != 0 || f.Status().Runs != 0 {
				t.Fatalf("gated scheduled run: %+v, %v", f.Status(), f.History())
			}
			got, err := f.Run(context.Background())
			if !errors.Is(err, want) {
				t.Fatalf("Run error = %v, want %v", err, want)
			}
			if want == nil {
				if got.Result != ResultSaved || hits.Load() != before+1 || len(f.History()) != 1 || !f.History()[0].Manual {
					t.Fatalf("manual run did not bypass window: %+v", got)
				}
			} else if hits.Load() != before || len(f.History()) != 0 || got.Runs != 0 {
				t.Fatalf("rejected manual run was recorded: %+v", got)
			}
		})
	}
}

func TestURLHookSelection(t *testing.T) {
	img := pngBytes(t, color.White)
	var fallbackHits, hookHits atomic.Int32
	serve := func(hits *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			w.Write(img)
		}))
	}
	fallback := serve(&fallbackHits)
	defer fallback.Close()
	hook := serve(&hookHits)
	defer hook.Close()

	t.Run("nil hook falls back to config", func(t *testing.T) {
		st, _ := store.New(t.TempDir())
		f := newTestFetcher(t, config.Magma{URL: fallback.URL}, t.TempDir(), st)
		if got, err := f.Run(context.Background()); err != nil || got.Result != ResultSaved {
			t.Fatalf("Run = %+v, %v", got, err)
		}
		if fallbackHits.Load() != 1 {
			t.Fatalf("fallback hits = %d", fallbackHits.Load())
		}
	})

	t.Run("hook overrides config", func(t *testing.T) {
		st, _ := store.New(t.TempDir())
		f := newTestFetcher(t, config.Magma{URL: fallback.URL}, t.TempDir(), st)
		calls := 0
		f.URL = func() string {
			calls++
			return hook.URL
		}
		if got, err := f.Run(context.Background()); err != nil || got.Result != ResultSaved {
			t.Fatalf("Run = %+v, %v", got, err)
		}
		if calls != 1 || hookHits.Load() != 1 || fallbackHits.Load() != 1 {
			t.Fatalf("hook calls=%d, hook hits=%d, fallback hits=%d", calls, hookHits.Load(), fallbackHits.Load())
		}
	})

	t.Run("hook supplies missing config URL", func(t *testing.T) {
		st, _ := store.New(t.TempDir())
		f := newTestFetcher(t, config.Magma{}, t.TempDir(), st)
		f.URL = func() string { return hook.URL }
		if got, err := f.Run(context.Background()); err != nil || got.Result != ResultSaved {
			t.Fatalf("Run = %+v, %v", got, err)
		}
		if hookHits.Load() != 2 {
			t.Fatalf("hook hits = %d", hookHits.Load())
		}
	})

	t.Run("empty hook suppresses config URL", func(t *testing.T) {
		st, _ := store.New(t.TempDir())
		f := newTestFetcher(t, config.Magma{URL: fallback.URL}, t.TempDir(), st)
		f.URL = func() string { return "" }
		f.RunScheduled(context.Background())
		if _, err := f.Run(context.Background()); !errors.Is(err, ErrNoURL) {
			t.Fatalf("Run error = %v", err)
		}
		if fallbackHits.Load() != 1 || len(f.History()) != 0 {
			t.Fatalf("empty hook fetched: hits=%d history=%+v", fallbackHits.Load(), f.History())
		}
	})
}

func TestBusyRun(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	img := pngBytes(t, color.White)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.Write(img)
	}))
	defer srv.Close()
	st, _ := store.New(t.TempDir())
	f := newTestFetcher(t, config.Magma{URL: srv.URL}, t.TempDir(), st)
	done := make(chan struct{})
	go func() { f.Run(context.Background()); close(done) }()
	<-entered
	_, runErr := f.Run(context.Background())
	close(release)
	<-done
	if !errors.Is(runErr, ErrBusy) {
		t.Fatalf("Run error = %v", runErr)
	}
	if len(f.History()) != 1 || f.cfg.URL != srv.URL {
		t.Fatalf("busy run changed state: %q, %+v", f.cfg.URL, f.History())
	}
}

func TestURLHookKeepsValidatorsWithActualURLAndDeduplication(t *testing.T) {
	red, blue := pngBytes(t, color.RGBA{255, 0, 0, 255}), pngBytes(t, color.RGBA{0, 0, 255, 255})
	seen := make(chan http.Header, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Mon, 02 Nov 2026 12:00:00 GMT")
		if r.URL.Path == "/blue" {
			w.Write(blue)
		} else {
			w.Write(red)
		}
	}))
	defer srv.Close()
	st, _ := store.New(t.TempDir())
	f := newTestFetcher(t, config.Magma{URL: "http://unused.example"}, t.TempDir(), st)
	path := ""
	f.URL = func() string { return srv.URL + path }
	f.Run(context.Background())
	<-seen
	last, recent := f.lastMD5, slices.Clone(f.recentMD5)
	for i, next := range []string{"", "/blue", ""} {
		path = next
		if i == 0 && (f.lastMD5 != last || !slices.Equal(f.recentMD5, recent)) {
			t.Fatal("URL hook changed deduplication state")
		}
		got, err := f.Run(context.Background())
		if err != nil || (i == 1 && got.Result != ResultSaved) || (i != 1 && got.Result != ResultUnchanged) {
			t.Fatalf("step %d: %+v, %v", i, got, err)
		}
		h := <-seen
		conditional := h.Get("If-None-Match") != "" || h.Get("If-Modified-Since") != ""
		if conditional != (i == 0) {
			t.Fatalf("step %d: validators = %v, conditional = %v", i, h, conditional)
		}
	}
	if st.Count() != 2 {
		t.Fatalf("deduplication lost: %d images", st.Count())
	}
}

func TestURLHookAndResetBackoffAllowScheduledRun(t *testing.T) {
	img := pngBytes(t, color.White)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/old" {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		w.Write(img)
	}))
	defer srv.Close()
	st, _ := store.New(t.TempDir())
	f := newTestFetcher(t, config.Magma{URL: "http://unused.example"}, t.TempDir(), st)
	path := "/old"
	f.URL = func() string { return srv.URL + path }
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	f.now = func() time.Time { return now }
	for range alertAfter {
		f.Run(context.Background())
	}
	if !f.Status().RetryAt.After(now) {
		t.Fatalf("backoff not established: %+v", f.Status())
	}
	f.RunScheduled(context.Background())
	if hits.Load() != alertAfter {
		t.Fatal("scheduled run ignored backoff")
	}
	path = "/new"
	f.ResetBackoff()
	if !f.Status().RetryAt.IsZero() {
		t.Fatalf("ResetBackoff kept old backoff: %+v", f.Status())
	}
	f.RunScheduled(context.Background())
	if hits.Load() != alertAfter+1 || f.Status().Result != ResultSaved || f.Status().Failures != 0 {
		t.Fatalf("new URL did not run immediately: %d hits, %+v", hits.Load(), f.Status())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestDrainWaitsForEnsure(t *testing.T) {
	// Channels and synctest keep this independent of image size and CPU speed.
	synctest.Test(t, func(t *testing.T) {
		st, _ := store.New(t.TempDir())
		f := newTestFetcher(t, config.Magma{URL: "http://board.example"}, t.TempDir(), st)
		img := pngBytes(t, color.White)
		releaseEnsure := make(chan struct{})
		f.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(img)), Request: r}, nil
		})
		ensured := make(chan string, 1)
		f.ensure = func(name string) error {
			<-releaseEnsure
			err := f.store.Ensure(name)
			ensured <- name
			return err
		}
		if got, err := f.Run(context.Background()); err != nil || got.Result != ResultSaved {
			t.Fatalf("Run = %+v, %v", got, err)
		}
		drained := make(chan struct{})
		go func() { f.Drain(); close(drained) }()
		synctest.Wait()
		select {
		case <-drained:
			t.Fatal("Drain returned before Ensure finished")
		default:
		}
		close(releaseEnsure)
		<-drained
		name := <-ensured
		if _, err := os.Stat(filepath.Join(st.Dir(), ".thumbs", name+".jpg")); err != nil {
			t.Fatalf("Ensure did not generate thumbnail: %v", err)
		}
	})
}

func TestFetcherDoesNotAlertForSharedToken(t *testing.T) {
	st, _ := store.New(t.TempDir())
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	f := newTestFetcher(t, config.Magma{Cookie: "AUTH_TOKEN=" + jwt(now.Add(time.Hour))}, t.TempDir(), st)
	f.now = func() time.Time { return now }
	alerts := 0
	alert := func(string, string) { alerts++ }
	stop := make(chan struct{})
	close(stop)
	f.WatchToken(stop, alert)
	if err := f.shared.Set("AUTH_TOKEN=" + jwt(now.Add(2*time.Hour))); err != nil {
		t.Fatal(err)
	}
	f.WatchToken(stop, alert)
	if alerts != 0 || f.Token().Cookie != "AUTH_TOKEN" {
		t.Fatalf("shared token was not inherited or was alerted: %+v, %d alerts", f.Token(), alerts)
	}
	if err := f.SetCookie("AUTH_TOKEN=" + jwt(now.Add(3*time.Hour))); err != nil {
		t.Fatal(err)
	}
	f.WatchToken(stop, alert)
	f.WatchToken(stop, alert)
	if alerts != 1 || !f.Token().Expires.Equal(now.Add(3*time.Hour)) {
		t.Fatalf("board token alert: %+v, %d alerts", f.Token(), alerts)
	}
	if err := f.SetCookie("AUTH_TOKEN=" + jwt(now.Add(4*time.Hour))); err != nil {
		t.Fatal(err)
	}
	f.WatchToken(stop, alert)
	if alerts != 2 {
		t.Fatalf("SetCookie did not reset reminders: %d", alerts)
	}
	if err := f.ClearCookie(); err != nil {
		t.Fatal(err)
	}
	f.WatchToken(stop, alert)
	if alerts != 2 || !f.Token().Expires.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("ClearCookie did not resume sharing: %+v, %d alerts", f.Token(), alerts)
	}
}

func TestSharedCookieUpdateDropsOldOutageOnRestart(t *testing.T) {
	st, _ := store.New(t.TempDir())
	cfg := config.Magma{URL: "http://127.0.0.1:1", Timeout: time.Second}
	f := newTestFetcher(t, cfg, t.TempDir(), st)
	clock := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	f.now = func() time.Time { return clock }
	f.shared.now = func() time.Time { return clock }
	for range 3 {
		f.Run(context.Background())
		clock = clock.Add(time.Minute)
	}
	if err := f.shared.Set("fresh"); err != nil {
		t.Fatal(err)
	}
	f.ResetBackoff()
	if !f.Status().RetryAt.IsZero() || f.Status().Failures != 3 {
		t.Fatalf("ResetBackoff = %+v", f.Status())
	}
	g := newTestFetcher(t, cfg, t.TempDir(), st)
	if g.Status().Failures != 0 || !g.Status().RetryAt.IsZero() || len(g.History()) != 3 {
		t.Fatalf("old shared cookie failures restored: %+v, %+v", g.Status(), g.History())
	}
}

func TestResetBackoffDoesNotWaitForOldCookieFetch(t *testing.T) {
	st, _ := store.New(t.TempDir())
	f := newTestFetcher(t, config.Magma{URL: "http://board.example"}, t.TempDir(), st)
	f.status.Failures = alertAfter - 1
	f.status.RetryAt = time.Now().Add(time.Hour)
	entered, release := make(chan struct{}), make(chan struct{})
	f.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-release
		return &http.Response{StatusCode: http.StatusForbidden, Status: "403 Forbidden", Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(nil)), Request: r}, nil
	})
	runDone := make(chan struct{})
	go func() { f.Run(context.Background()); close(runDone) }()
	<-entered
	err := f.shared.Set("fresh")
	resetDone := make(chan struct{})
	go func() {
		f.ResetBackoff()
		close(resetDone)
	}()
	select {
	case <-resetDone:
		if !f.Status().RetryAt.IsZero() {
			t.Errorf("ResetBackoff kept backoff: %+v", f.Status())
		}
	case <-time.After(time.Second):
		t.Error("ResetBackoff waited for the old fetch")
	}
	close(release)
	<-runDone
	<-resetDone
	if err != nil {
		t.Fatal(err)
	}
	if !f.Status().RetryAt.IsZero() || f.Status().Failures != alertAfter {
		t.Fatalf("old cookie failure reinstated backoff: %+v", f.Status())
	}
	if history := f.History(); len(history) != 1 || history[0].Result != ResultError || history[0].Code != CodeAuth {
		t.Fatalf("old cookie failure was not recorded: %+v", history)
	}
}
