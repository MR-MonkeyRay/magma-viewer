package fetcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"magma-viewer/internal/fsutil"
)

const cookieFile = ".state/board-cookie.json"

// ErrInvalidCookie rejects a pasted value that cannot be sent as a Cookie header.
var ErrInvalidCookie = errors.New("invalid cookie")

type cookieOverride struct {
	Cookie  string    `json:"cookie"`
	Updated time.Time `json:"updated"`
}

// CookieInfo tells admins which cookie is in use.
type CookieInfo struct {
	Source  string    `json:"source"` // "config", "page" or "board"
	Updated time.Time `json:"updated,omitzero"`
}

func (f *Fetcher) cookiePath() string { return filepath.Join(f.store.Dir(), cookieFile) }

// loadCookie restores a board override independently of the shared config.
func (f *Fetcher) loadCookie() {
	data, err := os.ReadFile(f.cookiePath())
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var o cookieOverride
	if err == nil {
		err = json.Unmarshal(data, &o)
	}
	if err != nil || o.Cookie == "" {
		log.Printf("fetcher: ignoring unreadable %s: %v", cookieFile, err)
		return
	}
	f.boardCookie, f.cookie = o.Cookie, CookieInfo{Source: "board", Updated: o.Updated}
}

// normalizeCookie accepts what an admin is likely to paste: a full Cookie
// header, "NAME=value" pairs, or just the AUTH_TOKEN value.
func normalizeCookie(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) >= 7 && strings.EqualFold(s[:7], "cookie:") {
		s = strings.TrimSpace(s[7:])
	}
	if s == "" || len(s) > 8192 || strings.ContainsAny(s, "\r\n\x00") {
		return "", ErrInvalidCookie
	}
	if !strings.Contains(s, "=") {
		s = "AUTH_TOKEN=" + s
	}
	cookies, err := http.ParseCookie(s)
	if err != nil {
		return "", ErrInvalidCookie
	}
	for _, c := range cookies {
		if c.Value != "" {
			return s, nil
		}
	}
	return "", ErrInvalidCookie
}

// SetCookie saves a board override and lets the next scheduled run try it.
func (f *Fetcher) SetCookie(raw string) error {
	cookie, err := normalizeCookie(raw)
	if err != nil {
		return err
	}
	f.runMu.Lock()
	defer f.runMu.Unlock()
	o := cookieOverride{Cookie: cookie, Updated: f.now().UTC()}
	data, _ := json.Marshal(o)
	if err := fsutil.WriteFile(f.cookiePath(), data, 0o600); err != nil {
		return fmt.Errorf("save cookie: %w", err)
	}
	f.statusMu.Lock()
	f.boardCookie, f.cookie = cookie, CookieInfo{Source: "board", Updated: o.Updated}
	f.tokenAlerted, f.status.RetryAt = time.Time{}, time.Time{}
	f.statusMu.Unlock()
	tok, _ := tokenFromCookie(cookie)
	logToken(tok, f.now())
	log.Printf("fetcher: board cookie updated")
	return nil
}

// ClearCookie removes the board override and resumes sharing credentials.
func (f *Fetcher) ClearCookie() error {
	f.runMu.Lock()
	defer f.runMu.Unlock()
	if err := os.Remove(f.cookiePath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear cookie: %w", err)
	}
	f.statusMu.Lock()
	f.boardCookie, f.cookie = "", CookieInfo{}
	f.tokenAlerted, f.status.RetryAt = time.Time{}, time.Time{}
	f.statusMu.Unlock()
	return nil
}

func (f *Fetcher) effectiveCookie() (string, CookieInfo) {
	f.statusMu.RLock()
	cookie, info := f.boardCookie, f.cookie
	f.statusMu.RUnlock()
	if cookie != "" {
		return cookie, info
	}
	return f.shared.Cookie()
}

// Cookie reports the effective source, including inherited credentials.
func (f *Fetcher) Cookie() CookieInfo {
	_, info := f.effectiveCookie()
	return info
}
