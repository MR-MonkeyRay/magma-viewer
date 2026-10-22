package fetcher

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"magma-viewer/internal/config"
	"magma-viewer/internal/store"
)

func pngBytes(t *testing.T, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := 0; x < 4; x++ {
		for y := 0; y < 4; y++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRunSavesOnlyWhenChanged(t *testing.T) {
	red, blue := pngBytes(t, color.RGBA{255, 0, 0, 255}), pngBytes(t, color.RGBA{0, 0, 255, 255})
	var body atomic.Value
	body.Store(red)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "sid=abc" || r.Header.Get("X-Test") != "1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Write(body.Load().([]byte))
	}))
	defer srv.Close()

	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Magma{URL: srv.URL, Cookie: "sid=abc", Headers: map[string]string{"X-Test": "1"}, Timeout: 5 * time.Second}
	f := New(cfg, t.TempDir(), st)
	clock := time.Date(2026, 10, 20, 12, 0, 0, 0, time.Local)
	// Every run lands in the same second, so ordering relies on the mtime tie-breaker.
	f.now = func() time.Time { return clock }

	steps := []struct {
		body []byte
		want string
		n    int
	}{
		{red, ResultSaved, 1},
		{red, ResultUnchanged, 1},
		{blue, ResultSaved, 2},
		{red, ResultSaved, 3}, // only compared with the previous image
	}
	for i, s := range steps {
		body.Store(s.body)
		got := f.Run(context.Background())
		if got.Result != s.want || st.Count() != s.n {
			t.Fatalf("step %d: result=%s (%s) count=%d, want %s/%d", i, got.Result, got.Message, st.Count(), s.want, s.n)
		}
	}
	latest, _ := st.Latest()
	if !strings.HasPrefix(latest.Name, "20261020-1200") || !strings.HasSuffix(latest.Name, ".png") {
		t.Fatalf("unexpected name %s", latest.Name)
	}

	// A new fetcher picks up the last md5 from disk.
	f2 := New(cfg, t.TempDir(), st)
	if got := f2.Run(context.Background()); got.Result != ResultUnchanged {
		t.Fatalf("restart: got %s", got.Result)
	}
}

func TestRunRejectsNonImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("<html>please log in</html>"))
	}))
	defer srv.Close()
	dir := t.TempDir()
	st, _ := store.New(dir)
	tmp := t.TempDir()
	f := New(config.Magma{URL: srv.URL, Timeout: 5 * time.Second}, tmp, st)
	got := f.Run(context.Background())
	if got.Result != ResultError || !strings.Contains(got.Message, "not an image") {
		t.Fatalf("got %+v", got)
	}
	if st.Count() != 0 {
		t.Fatal("non-image was stored")
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestRunReportsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()
	st, _ := store.New(t.TempDir())
	f := New(config.Magma{URL: srv.URL, Timeout: 5 * time.Second}, t.TempDir(), st)
	if got := f.Run(context.Background()); got.Result != ResultError || !strings.Contains(got.Message, "401") {
		t.Fatalf("got %+v", got)
	}
}

func TestPauseSkipsScheduledRunsAndPersists(t *testing.T) {
	var hits atomic.Int32
	img := pngBytes(t, color.RGBA{0, 255, 0, 255})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write(img)
	}))
	defer srv.Close()
	st, _ := store.New(t.TempDir())
	cfg := config.Magma{URL: srv.URL, Timeout: 5 * time.Second}
	f := New(cfg, t.TempDir(), st)

	if err := f.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	f.RunScheduled(context.Background())
	if hits.Load() != 0 {
		t.Fatal("scheduled run fetched while paused")
	}
	// Manual runs still work while paused.
	if got := f.Run(context.Background()); got.Result != ResultSaved || got.LastFetched.IsZero() {
		t.Fatalf("manual run: %+v", got)
	}
	if err := st.Rescan(); err != nil || st.Count() != 1 {
		t.Fatalf("state file indexed as image: count=%d err=%v", st.Count(), err)
	}

	f2 := New(cfg, t.TempDir(), st)
	if !f2.Status().Paused {
		t.Fatal("pause not restored after restart")
	}
	if err := f2.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	f2.RunScheduled(context.Background())
	if hits.Load() != 2 {
		t.Fatalf("resumed scheduled run: hits=%d", hits.Load())
	}
	if New(cfg, t.TempDir(), st).Status().Paused {
		t.Fatal("resume not persisted")
	}
}

func TestQueuedScheduledRunHonoursPause(t *testing.T) {
	var hits atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	img := pngBytes(t, color.RGBA{0, 0, 0, 255})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			close(entered)
			<-release
		}
		w.Write(img)
	}))
	defer srv.Close()
	st, _ := store.New(t.TempDir())
	f := New(config.Magma{URL: srv.URL, Timeout: 5 * time.Second}, t.TempDir(), st)

	manual := make(chan struct{})
	go func() { f.Run(context.Background()); close(manual) }()
	<-entered
	scheduled := make(chan struct{})
	go func() { f.RunScheduled(context.Background()); close(scheduled) }()
	if err := f.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-manual
	<-scheduled
	if n := hits.Load(); n != 1 {
		t.Fatalf("queued scheduled run fetched after pause: hits=%d", n)
	}
}

func TestLastFetchedSurvivesRestart(t *testing.T) {
	img := pngBytes(t, color.RGBA{9, 9, 9, 255})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(img) }))
	defer srv.Close()
	st, _ := store.New(t.TempDir())
	cfg := config.Magma{URL: srv.URL, Timeout: 5 * time.Second}
	f := New(cfg, t.TempDir(), st)
	clock := time.Date(2026, 10, 20, 12, 0, 0, 0, time.Local)
	f.now = func() time.Time { return clock }
	f.Run(context.Background())
	clock = clock.Add(time.Minute)
	if got := f.Run(context.Background()); got.Result != ResultUnchanged || !got.LastFetched.Equal(clock) {
		t.Fatalf("unchanged run: %+v", got)
	}

	// Errors leave the last successful fetch time alone.
	f.cfg.URL = "http://127.0.0.1:1"
	clock = clock.Add(time.Minute)
	if got := f.Run(context.Background()); got.Result != ResultError || !got.LastFetched.Equal(clock.Add(-time.Minute)) {
		t.Fatalf("error run: %+v", got)
	}

	if got := New(cfg, t.TempDir(), st).Status().LastFetched; !got.Equal(clock.Add(-time.Minute)) {
		t.Fatalf("restored last_fetched=%v", got)
	}
}

func TestCorruptStateFile(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(dir+"/.state", 0o755)
	os.WriteFile(dir+"/"+stateFile, []byte("{not json"), 0o644)
	st, _ := store.New(dir)
	s := New(config.Magma{URL: "http://127.0.0.1:1"}, t.TempDir(), st).Status()
	if s.Paused || !s.LastFetched.IsZero() {
		t.Fatalf("corrupt state: %+v", s)
	}
}

func TestNoFetchMeansNoLastFetched(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/20261020-120000_"+strings.Repeat("a", 32)+".png", pngBytes(t, color.White), 0o644)
	st, _ := store.New(dir)
	// An imported image is not a backend fetch.
	if s := New(config.Magma{URL: "http://127.0.0.1:1"}, t.TempDir(), st).Status(); !s.LastFetched.IsZero() {
		t.Fatalf("last_fetched=%v", s.LastFetched)
	}
}

func TestConditionalRequestAnswered304(t *testing.T) {
	img := pngBytes(t, color.RGBA{1, 2, 3, 255})
	var conditional atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			conditional.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Write(img)
	}))
	defer srv.Close()
	st, _ := store.New(t.TempDir())
	f := New(config.Magma{URL: srv.URL, Timeout: 5 * time.Second}, t.TempDir(), st)
	if got := f.Run(context.Background()); got.Result != ResultSaved {
		t.Fatalf("first run: %+v", got)
	}
	if got := f.Run(context.Background()); got.Result != ResultUnchanged || got.LastFetched.IsZero() {
		t.Fatalf("304 run: %+v", got)
	}
	if conditional.Load() != 1 || st.Count() != 1 {
		t.Fatalf("conditional requests %d, images %d", conditional.Load(), st.Count())
	}
}

func TestBackoffAfterRepeatedFailures(t *testing.T) {
	var hits atomic.Int32
	var fail atomic.Bool
	fail.Store(true)
	img := pngBytes(t, color.RGBA{4, 5, 6, 255})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if fail.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		w.Write(img)
	}))
	defer srv.Close()
	st, _ := store.New(t.TempDir())
	f := New(config.Magma{URL: srv.URL, Timeout: 5 * time.Second}, t.TempDir(), st)
	clock := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	f.now = func() time.Time { return clock }

	for range 3 {
		f.RunScheduled(context.Background())
		clock = clock.Add(time.Minute)
	}
	got := f.Status()
	if got.Failures != 3 || !got.RetryAt.Equal(time.Date(2026, 10, 20, 12, 3, 0, 0, time.UTC)) {
		t.Fatalf("after 3 failures: failures=%d retry_at=%v", got.Failures, got.RetryAt)
	}
	// A scheduled run before retry_at is skipped; the one due at retry_at goes ahead.
	clock = got.RetryAt.Add(-30 * time.Second)
	f.RunScheduled(context.Background())
	if hits.Load() != 3 {
		t.Fatalf("scheduled run during backoff hit the server (%d hits)", hits.Load())
	}
	clock = got.RetryAt.Add(-time.Second) // cron jitter: a hair early still counts
	f.RunScheduled(context.Background())
	if hits.Load() != 4 {
		t.Fatal("run due at retry_at was skipped")
	}
	f.Run(context.Background()) // manual runs ignore the backoff
	if hits.Load() != 5 {
		t.Fatal("manual run was held back")
	}
	fail.Store(false)
	clock = clock.Add(time.Hour)
	if got := f.Run(context.Background()); got.Failures != 0 || !got.RetryAt.IsZero() || got.Result != ResultSaved {
		t.Fatalf("after recovery: %+v", got)
	}
}

func TestBackoffSchedule(t *testing.T) {
	for n, want := range map[int]time.Duration{0: 0, 2: 0, 3: time.Minute, 4: 2 * time.Minute, 7: 16 * time.Minute, 8: 30 * time.Minute, 1000: 30 * time.Minute} {
		if got := backoff(n); got != want {
			t.Errorf("backoff(%d) = %v, want %v", n, got, want)
		}
	}
}

func TestRetryAtCountsFromTheEndOfASlowFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer srv.Close()
	st, _ := store.New(t.TempDir())
	f := New(config.Magma{URL: srv.URL, Timeout: 5 * time.Second}, t.TempDir(), st)
	clock := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	calls := 0
	f.now = func() time.Time {
		calls++
		if calls%2 == 0 {
			return clock.Add(90 * time.Second) // each run ends 90s after it started
		}
		return clock
	}
	for range 3 {
		f.Run(context.Background())
	}
	if got := f.Status().RetryAt; !got.Equal(clock.Add(90*time.Second + time.Minute)) {
		t.Fatalf("retry_at %v, want a minute after the failure ended", got)
	}
}

func TestValidatorsStayWithTheirURL(t *testing.T) {
	red, blue := pngBytes(t, color.RGBA{255, 0, 0, 255}), pngBytes(t, color.RGBA{0, 0, 255, 255})
	var target atomic.Value
	target.Store("/old")
	mux := http.NewServeMux()
	mux.HandleFunc("/render", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.Load().(string), http.StatusFound)
	})
	// Both objects use the same ETag, which is legal: validators are per URL.
	serve := func(img []byte) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("If-None-Match") == `"v1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", `"v1"`)
			w.Write(img)
		}
	}
	mux.HandleFunc("/old", serve(red))
	mux.HandleFunc("/new", serve(blue))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	st, _ := store.New(t.TempDir())
	f := New(config.Magma{URL: srv.URL + "/render", Timeout: 5 * time.Second}, t.TempDir(), st)
	clock := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	f.now = func() time.Time { clock = clock.Add(time.Minute); return clock }
	if got := f.Run(context.Background()); got.Result != ResultSaved {
		t.Fatalf("first: %+v", got)
	}
	if got := f.Run(context.Background()); got.Result != ResultUnchanged {
		t.Fatalf("same target: %+v", got)
	}
	target.Store("/new")
	if got := f.Run(context.Background()); got.Result != ResultSaved || st.Count() != 2 {
		t.Fatalf("new target judged by the old validator: %+v, %d images", got, st.Count())
	}
}
