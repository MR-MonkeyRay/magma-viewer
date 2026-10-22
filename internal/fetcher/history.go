package fetcher

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	"magma-viewer/internal/fsutil"
)

// historyFile keeps the latest runs so admins can see what happened while
// they were away, and so a restart does not forget an ongoing outage.
const (
	historyFile = ".state/history.json"
	historySize = 50
)

// Run records one fetch attempt.
type Run struct {
	Time    time.Time `json:"time"`
	Manual  bool      `json:"manual,omitempty"`
	Result  string    `json:"result"`
	Code    string    `json:"code,omitempty"`
	Message string    `json:"message,omitempty"`
	File    string    `json:"file,omitempty"`
	HTTP    int       `json:"http,omitempty"`  // final response status, 0 when none arrived
	Bytes   int64     `json:"bytes,omitempty"` // downloaded body size
	Millis  int64     `json:"ms"`
}

func (f *Fetcher) historyPath() string { return filepath.Join(f.store.Dir(), historyFile) }

// loadHistory restores the recorded runs, newest first, and the outage they
// may describe: consecutive failures at the head keep their backoff.
func (f *Fetcher) loadHistory() {
	data, err := os.ReadFile(f.historyPath())
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("fetcher: read %s: %v", historyFile, err)
		}
		return
	}
	if err := json.Unmarshal(data, &f.history); err != nil {
		log.Printf("fetcher: read %s: %v", historyFile, err)
		f.history = nil
		return
	}
	// Failures from before a cookie update say nothing about the new cookie.
	for _, r := range f.history {
		if r.Result != ResultError || r.Time.Before(f.cookie.Updated) {
			break
		}
		f.status.Failures++
	}
	if f.status.Failures > 0 {
		last := f.history[0]
		f.status.Result, f.status.Code, f.status.Message, f.status.LastRun = last.Result, last.Code, last.Message, last.Time
		if d := backoff(f.status.Failures); d > 0 {
			f.status.RetryAt = last.Time.Add(time.Duration(last.Millis)*time.Millisecond + d)
		}
	}
}

// record adds r to the history and persists it; the caller holds runMu.
func (f *Fetcher) record(r Run) {
	f.statusMu.Lock()
	f.history = append([]Run{r}, f.history[:min(len(f.history), historySize-1)]...)
	data, err := json.Marshal(f.history)
	f.statusMu.Unlock()
	if err == nil {
		err = fsutil.WriteFile(f.historyPath(), data, 0o644)
	}
	if err != nil {
		log.Printf("fetcher: save %s: %v", historyFile, err)
	}
}

// History returns the recorded runs, newest first.
func (f *Fetcher) History() []Run {
	f.statusMu.RLock()
	defer f.statusMu.RUnlock()
	return append([]Run{}, f.history...)
}
