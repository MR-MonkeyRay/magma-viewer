// Package fetcher downloads the magma canvas image and stores it when it differs
// from the previously saved one.
package fetcher

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"magma-viewer/internal/config"
	"magma-viewer/internal/fsutil"
	"magma-viewer/internal/store"
)

// maxImageBytes guards against unbounded responses.
const maxImageBytes = 200 << 20

const (
	ResultSaved     = "saved"
	ResultUnchanged = "unchanged"
	ResultError     = "error"
)

// stateFile keeps the pause switch and last fetch time across restarts. It
// lives in a dot-subdirectory: the store skips it, and rewriting it does not
// touch the storage dir mtime that triggers a rescan.
const stateFile = ".state/fetcher.json"

var contentTypeExt = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

type Status struct {
	Paused      bool      `json:"paused"`
	LastRun     time.Time `json:"last_run"`
	Result      string    `json:"result"`
	Message     string    `json:"message,omitempty"`
	File        string    `json:"file,omitempty"`
	LastFetched time.Time `json:"last_fetched"` // last successful download, saved or not
	LastSaved   time.Time `json:"last_saved"`
	Runs        int       `json:"runs"`
	Saved       int       `json:"saved"`
	Failures    int       `json:"failures"`          // consecutive failed runs
	RetryAt     time.Time `json:"retry_at,omitzero"` // scheduled runs wait until then after repeated failures
}

type savedState struct {
	Paused      bool      `json:"paused"`
	LastFetched time.Time `json:"last_fetched"`
}

type Fetcher struct {
	cfg     config.Magma
	tempDir string
	store   *store.Store
	client  *http.Client
	now     func() time.Time
	token   TokenInfo

	runMu   sync.Mutex // serialises runs (cron + manual trigger)
	lastMD5 string
	// Validators of the last good response and the URL that sent them (the
	// end of any redirect), so an unchanged image can be answered with 304.
	etag, lastModified, validatorURL string

	stateMu sync.Mutex // serialises writes of stateFile

	statusMu sync.RWMutex
	status   Status
}

func New(cfg config.Magma, tempDir string, st *store.Store) *Fetcher {
	f := &Fetcher{
		cfg:     cfg,
		tempDir: tempDir,
		store:   st,
		client:  &http.Client{Timeout: cfg.Timeout},
		now:     time.Now,
	}
	if tok, ok := tokenFromCookie(cfg.Cookie); ok {
		f.token = tok
		switch left := time.Until(tok.Expires); {
		case left <= 0:
			log.Printf("fetcher: WARNING cookie %s expired at %s, update magma.cookie", tok.Cookie, tok.Expires.Format(time.DateTime))
		case left < 7*24*time.Hour:
			log.Printf("fetcher: WARNING cookie %s expires at %s", tok.Cookie, tok.Expires.Format(time.DateTime))
		default:
			log.Printf("fetcher: cookie %s valid until %s", tok.Cookie, tok.Expires.Format(time.DateTime))
		}
	}
	if it, ok := st.Latest(); ok {
		sum, err := st.FileMD5(it)
		if err != nil {
			log.Printf("fetcher: md5 of latest image %s: %v", it.Name, err)
		}
		f.lastMD5 = sum
		f.status.LastSaved = it.Time
	}
	if data, err := os.ReadFile(f.statePath()); err == nil {
		var s savedState
		if err := json.Unmarshal(data, &s); err != nil {
			log.Printf("fetcher: read %s: %v", stateFile, err)
		}
		f.status.Paused, f.status.LastFetched = s.Paused, s.LastFetched
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Printf("fetcher: read %s: %v", stateFile, err)
	}
	if f.status.Paused {
		log.Printf("fetcher: scheduled runs are paused")
	}
	return f
}

func (f *Fetcher) statePath() string { return filepath.Join(f.store.Dir(), stateFile) }

// writeState persists s; the caller holds stateMu.
func (f *Fetcher) writeState(s savedState) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return fsutil.WriteFile(f.statePath(), data, 0o644)
}

// SetPaused switches scheduled runs off or on and persists the choice.
// Manual runs are not affected.
func (f *Fetcher) SetPaused(paused bool) error {
	f.stateMu.Lock()
	defer f.stateMu.Unlock()
	if err := f.writeState(savedState{Paused: paused, LastFetched: f.Status().LastFetched}); err != nil {
		return err
	}
	f.statusMu.Lock()
	f.status.Paused = paused
	f.statusMu.Unlock()
	log.Printf("fetcher: scheduled runs paused=%v", paused)
	return nil
}

// saveLastFetched persists the current status after a successful download.
func (f *Fetcher) saveLastFetched() {
	f.stateMu.Lock()
	defer f.stateMu.Unlock()
	st := f.Status()
	if err := f.writeState(savedState{Paused: st.Paused, LastFetched: st.LastFetched}); err != nil {
		log.Printf("fetcher: save %s: %v", stateFile, err)
	}
}

// RunScheduled is the cron entry point; it does nothing while paused or while
// backing off after repeated failures. The checks happen under runMu so a run
// queued behind a manual fetch still honours a pause made in the meantime.
func (f *Fetcher) RunScheduled(ctx context.Context) {
	f.runMu.Lock()
	defer f.runMu.Unlock()
	st := f.Status()
	// The tolerance keeps a retry due on the very tick it was aimed at from
	// being skipped by a few milliseconds of jitter.
	if st.Paused || f.now().Add(2*time.Second).Before(st.RetryAt) {
		return
	}
	f.run(ctx)
}

// backoff is how long scheduled runs pause after the given number of
// consecutive failures: not at all for the first two, then from one minute
// doubling up to half an hour. A schedule slower than that is unaffected.
func backoff(failures int) time.Duration {
	if failures < 3 {
		return 0
	}
	return min(time.Minute<<min(failures-3, 10), 30*time.Minute)
}

func (f *Fetcher) Status() Status {
	f.statusMu.RLock()
	defer f.statusMu.RUnlock()
	return f.status
}

// Run performs one fetch cycle and returns the updated status.
func (f *Fetcher) Run(ctx context.Context) Status {
	f.runMu.Lock()
	defer f.runMu.Unlock()
	return f.run(ctx)
}

// run does the work of Run; the caller holds runMu.
func (f *Fetcher) run(ctx context.Context) Status {
	started := f.now()
	file, err := f.fetchOnce(ctx, started)
	if err == nil {
		defer f.saveLastFetched() // runs after statusMu is released
	}

	f.statusMu.Lock()
	defer f.statusMu.Unlock()
	st := &f.status
	prevMsg := st.Message
	st.LastRun, st.File, st.Message, st.RetryAt = started, "", "", time.Time{}
	st.Runs++
	if err != nil {
		st.Result, st.Message = ResultError, err.Error()
		st.Failures++
		if d := backoff(st.Failures); d > 0 {
			st.RetryAt = f.now().Add(d) // from the end: a slow failure must not eat the pause
		}
		// A lasting outage (say an expired cookie) would otherwise log the same line every run.
		switch {
		case st.Message != prevMsg:
			log.Printf("fetcher: %v", err)
		case st.Failures%60 == 0:
			log.Printf("fetcher: still failing after %d attempts: %v", st.Failures, err)
		}
		return *st
	}
	if st.Failures > 0 {
		log.Printf("fetcher: recovered after %d failed attempts", st.Failures)
		st.Failures = 0
	}
	if file == "" {
		st.Result, st.LastFetched = ResultUnchanged, started
	} else {
		st.Result, st.File, st.LastSaved, st.LastFetched = ResultSaved, file, started, started
		st.Saved++
		log.Printf("fetcher: saved %s", file)
	}
	return *st
}

// fetchOnce returns the stored file name, or "" when the image is unchanged.
func (f *Fetcher) fetchOnce(ctx context.Context, now time.Time) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.cfg.URL, nil)
	if err != nil {
		return "", err
	}
	for k, v := range f.cfg.Headers {
		req.Header.Set(k, v)
	}
	if f.cfg.Cookie != "" {
		req.Header.Set("Cookie", f.cfg.Cookie)
	}
	// Validators only mean something to the URL that issued them, and only
	// while the image they describe is the latest saved one.
	conditional := func(r *http.Request) {
		r.Header.Del("If-None-Match")
		r.Header.Del("If-Modified-Since")
		if f.lastMD5 == "" || r.URL.String() != f.validatorURL {
			return
		}
		if f.etag != "" {
			r.Header.Set("If-None-Match", f.etag)
		}
		if f.lastModified != "" {
			r.Header.Set("If-Modified-Since", f.lastModified)
		}
	}
	conditional(req)
	client := *f.client
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		conditional(r) // redirects copy the first request's headers
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified && f.lastMD5 != "" {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		msg := strings.TrimSpace(string(snippet))
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return "", fmt.Errorf("unexpected status %s: %s (cookie expired or invalid?)", resp.Status, msg)
		}
		return "", fmt.Errorf("unexpected status %s: %s", resp.Status, msg)
	}

	tmp, err := os.CreateTemp(f.tempDir, "magma-*.download")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	h := md5.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	if n > maxImageBytes {
		return "", fmt.Errorf("response larger than %d bytes", maxImageBytes)
	}

	ext, err := sniffExt(tmp)
	if err != nil {
		return "", err
	}

	// Validators are kept only once this image is safely the latest saved one;
	// keeping them after a failed save would turn every later answer into a 304.
	remember := func() {
		f.etag, f.lastModified = resp.Header.Get("ETag"), resp.Header.Get("Last-Modified")
		f.validatorURL = resp.Request.URL.String()
	}

	sum := hex.EncodeToString(h.Sum(nil))
	if sum == f.lastMD5 {
		remember()
		return "", nil
	}

	name := f.store.FileName(now, sum, ext)
	// The same content may come back within one second (A -> B -> A).
	for _, exists := f.store.Get(name); exists; _, exists = f.store.Get(name) {
		now = now.Add(time.Second)
		name = f.store.FileName(now, sum, ext)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if err := f.store.Import(tmp, name); err != nil {
		return "", fmt.Errorf("save: %w", err)
	}
	f.lastMD5 = sum
	remember()
	// Render thumbnails now so the first visitor does not wait for a 4K decode.
	go func() {
		if err := f.store.Ensure(name); err != nil {
			log.Printf("fetcher: %v", err)
		}
	}()
	return name, nil
}

// sniffExt detects the image type from leading bytes; a login page may return
// 200 HTML, so the Content-Type header is not trusted.
func sniffExt(f *os.File) (string, error) {
	head := make([]byte, 512)
	n, err := f.ReadAt(head, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if n == 0 {
		return "", errors.New("empty response body")
	}
	ct := http.DetectContentType(head[:n])
	ext, ok := contentTypeExt[ct]
	if !ok {
		return "", fmt.Errorf("response is %s, not an image (cookie expired or wrong url?)", ct)
	}
	return ext, nil
}
