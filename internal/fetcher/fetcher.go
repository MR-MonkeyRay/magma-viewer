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
	path := f.statePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	part := path + ".part"
	if err := os.WriteFile(part, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(part, path); err != nil {
		os.Remove(part)
		return err
	}
	return nil
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

// RunScheduled is the cron entry point; it does nothing while paused. The
// check happens under runMu so a run queued behind a manual fetch still
// honours a pause made in the meantime.
func (f *Fetcher) RunScheduled(ctx context.Context) {
	f.runMu.Lock()
	defer f.runMu.Unlock()
	if f.Status().Paused {
		return
	}
	f.run(ctx)
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
	st.LastRun, st.File, st.Message = started, "", ""
	st.Runs++
	switch {
	case err != nil:
		st.Result, st.Message = ResultError, err.Error()
		log.Printf("fetcher: %v", err)
	case file == "":
		st.Result, st.LastFetched = ResultUnchanged, started
	default:
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
	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
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

	sum := hex.EncodeToString(h.Sum(nil))
	if sum == f.lastMD5 {
		return "", nil
	}

	name := store.FileName(now, sum, ext)
	// The same content may come back within one second (A -> B -> A).
	for _, exists := f.store.Get(name); exists; _, exists = f.store.Get(name) {
		now = now.Add(time.Second)
		name = store.FileName(now, sum, ext)
	}
	if err := copyInto(tmp, filepath.Join(f.store.Dir(), name)); err != nil {
		return "", fmt.Errorf("save: %w", err)
	}
	f.lastMD5 = sum
	if err := f.store.Add(name); err != nil {
		return "", fmt.Errorf("index: %w", err)
	}
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

// copyInto copies src to dst via a temp file, as /tmp may be on another device.
func copyInto(src *os.File, dst string) error {
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return err
	}
	part := dst + ".part"
	out, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		os.Remove(part)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(part)
		return err
	}
	return os.Rename(part, dst)
}
