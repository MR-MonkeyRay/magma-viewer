package fetcher

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"magma-viewer/internal/fsutil"
)

// cookieFile holds a cookie pasted on the admin page, which replaces
// magma.cookie without a restart.
const cookieFile = ".state/cookie.json"

// ErrInvalidCookie rejects a pasted value that cannot be sent as a Cookie header.
var ErrInvalidCookie = errors.New("invalid cookie")

type cookieOverride struct {
	Cookie string `json:"cookie"`
	// ConfigSHA identifies the magma.cookie it replaced: once the config is
	// edited the operator's newer choice wins and the override is dropped.
	ConfigSHA string    `json:"config_sha256"`
	Updated   time.Time `json:"updated"`
}

// CookieInfo tells admins which cookie is in use.
type CookieInfo struct {
	Source  string    `json:"source"` // "config" or "page"
	Updated time.Time `json:"updated,omitzero"`
}

func (f *Fetcher) cookiePath() string { return filepath.Join(f.store.Dir(), cookieFile) }

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// loadCookie applies a saved override that still matches the config.
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
	if o.ConfigSHA != sha(f.cfg.Cookie) {
		log.Printf("fetcher: magma.cookie changed since the cookie was updated on the page; using the config")
		os.Remove(f.cookiePath())
		return
	}
	f.cfg.Cookie, f.cookie = o.Cookie, CookieInfo{Source: "page", Updated: o.Updated}
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

// SetCookie switches to a new cookie at once and keeps it across restarts.
// A pending backoff is lifted so the next scheduled run tries it.
func (f *Fetcher) SetCookie(raw string) error {
	cookie, err := normalizeCookie(raw)
	if err != nil {
		return err
	}
	f.runMu.Lock()
	defer f.runMu.Unlock()
	o := cookieOverride{Cookie: cookie, ConfigSHA: sha(f.configCookie), Updated: f.now().UTC()} // full precision: history entries are compared against it
	data, _ := json.Marshal(o)
	if err := fsutil.WriteFile(f.cookiePath(), data, 0o600); err != nil {
		return fmt.Errorf("save cookie: %w", err)
	}
	f.cfg.Cookie = cookie
	tok, _ := tokenFromCookie(cookie)
	// One critical section, so WatchToken never pairs the old token with a reset reminder.
	f.statusMu.Lock()
	f.cookie = CookieInfo{Source: "page", Updated: o.Updated}
	f.token, f.tokenAlerted = tok, time.Time{}
	f.status.RetryAt = time.Time{}
	f.statusMu.Unlock()
	f.logToken(tok)
	log.Printf("fetcher: cookie updated from the admin page")
	return nil
}

// Cookie reports where the cookie in use came from.
func (f *Fetcher) Cookie() CookieInfo {
	f.statusMu.RLock()
	defer f.statusMu.RUnlock()
	return f.cookie
}

// setToken reads the JWT expiry out of cookie and logs how long it has left.
func (f *Fetcher) setToken(cookie string) {
	tok, _ := tokenFromCookie(cookie)
	f.statusMu.Lock()
	f.token = tok // zero when the cookie holds no JWT
	f.statusMu.Unlock()
	f.logToken(tok)
}

func (f *Fetcher) logToken(tok TokenInfo) {
	if tok.Cookie == "" {
		return
	}
	switch left := tok.Expires.Sub(f.now()); {
	case left <= 0:
		log.Printf("fetcher: WARNING cookie %s expired at %s, update magma.cookie", tok.Cookie, tok.Expires.Format(time.DateTime))
	case left < tokenWarning:
		log.Printf("fetcher: WARNING cookie %s expires at %s", tok.Cookie, tok.Expires.Format(time.DateTime))
	default:
		log.Printf("fetcher: cookie %s valid until %s", tok.Cookie, tok.Expires.Format(time.DateTime))
	}
}

// tokenWarning is how early an expiring cookie is flagged.
const tokenWarning = 7 * 24 * time.Hour

// WatchToken alerts once a day while the cookie expires within tokenWarning.
func (f *Fetcher) WatchToken(stop <-chan struct{}) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		f.checkToken()
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

func (f *Fetcher) checkToken() {
	if f.Alert == nil {
		return
	}
	now := f.now()
	f.statusMu.Lock()
	tok, last := f.token, f.tokenAlerted
	due := tok.Cookie != "" && tok.Expires.Sub(now) < tokenWarning && now.Sub(last) >= 24*time.Hour
	if due {
		f.tokenAlerted = now
	}
	f.statusMu.Unlock()
	if !due {
		return
	}
	if left := tok.Expires.Sub(now); left <= 0 {
		f.Alert(AlertCookieExpiring, fmt.Sprintf("Cookie %s 已于 %s 过期，请在管理面板更新。", tok.Cookie, tok.Expires.Format(time.DateTime)))
	} else {
		f.Alert(AlertCookieExpiring, fmt.Sprintf("Cookie %s 将在 %s 过期（约 %d 天后），请及时更新。", tok.Cookie, tok.Expires.Format(time.DateTime), int(math.Ceil(left.Hours()/24))))
	}
}
