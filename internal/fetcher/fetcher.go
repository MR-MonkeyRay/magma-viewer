// Package fetcher downloads the magma canvas image and stores it when it differs
// from the previously saved one.
package fetcher

import (
	"context"
	"crypto/md5"
	"encoding/hex"
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

var contentTypeExt = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

type Status struct {
	LastRun   time.Time `json:"last_run"`
	Result    string    `json:"result"`
	Message   string    `json:"message,omitempty"`
	File      string    `json:"file,omitempty"`
	LastSaved time.Time `json:"last_saved"`
	Runs      int       `json:"runs"`
	Saved     int       `json:"saved"`
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
	return f
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

	started := f.now()
	file, err := f.fetchOnce(ctx, started)

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
		st.Result = ResultUnchanged
	default:
		st.Result, st.File, st.LastSaved = ResultSaved, file, started
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
