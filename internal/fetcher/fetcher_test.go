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
