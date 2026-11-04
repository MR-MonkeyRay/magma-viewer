package fetcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"magma-viewer/internal/fsutil"
)

// Credentials is the cookie every board shares unless it has its own.
type Credentials struct {
	mu           sync.RWMutex
	path         string
	cookie       string
	info         CookieInfo
	tokenAlerted time.Time
	now          func() time.Time
}

type credentialsOverride struct {
	Cookie  string    `json:"cookie"`
	Updated time.Time `json:"updated"`
}

// NewCredentials loads the shared cookie saved from the admin page.
func NewCredentials(path string) *Credentials {
	c := &Credentials{path: path, info: CookieInfo{Source: "none"}, now: time.Now}
	c.load()
	logToken(c.Token(), c.now())
	return c
}

func (c *Credentials) load() {
	data, err := os.ReadFile(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var o credentialsOverride
	if err == nil {
		err = json.Unmarshal(data, &o)
	}
	if err != nil || o.Cookie == "" {
		log.Printf("fetcher: ignoring unreadable shared cookie file %s: %v", c.path, err)
		return
	}
	c.cookie = o.Cookie
	c.info = CookieInfo{Source: "page", Updated: o.Updated}
}

// Cookie returns the shared cookie and where it came from.
func (c *Credentials) Cookie() (string, CookieInfo) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cookie, c.info
}

// Set replaces the shared cookie and persists it.
func (c *Credentials) Set(raw string) error {
	cookie, err := normalizeCookie(raw)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	o := credentialsOverride{Cookie: cookie, Updated: c.now().UTC()}
	data, err := json.Marshal(o)
	if err != nil {
		return fmt.Errorf("encode shared cookie: %w", err)
	}
	if err := fsutil.WriteFile(c.path, data, 0o600); err != nil {
		return fmt.Errorf("save shared cookie: %w", err)
	}
	c.cookie = cookie
	c.info = CookieInfo{Source: "page", Updated: o.Updated}
	c.tokenAlerted = time.Time{}
	tok, _ := tokenFromCookie(c.cookie)
	logToken(tok, c.now())
	log.Printf("fetcher: shared cookie updated from the admin page")
	return nil
}

// Clear removes the shared cookie.
func (c *Credentials) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.Remove(c.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear shared cookie: %w", err)
	}
	c.cookie = ""
	c.info = CookieInfo{Source: "none"}
	c.tokenAlerted = time.Time{}
	tok, _ := tokenFromCookie(c.cookie)
	logToken(tok, c.now())
	return nil
}

// Token reports the JWT expiry in the shared cookie, if present.
func (c *Credentials) Token() TokenInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	tok, _ := tokenFromCookie(c.cookie)
	return tok
}

// WatchToken alerts once a day while the shared cookie is near expiry.
func (c *Credentials) WatchToken(stop <-chan struct{}, alert func(event, message string)) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		c.checkToken(alert)
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

func (c *Credentials) checkToken(alert func(event, message string)) {
	if alert == nil {
		return
	}
	c.mu.Lock()
	now := c.now()
	tok, _ := tokenFromCookie(c.cookie)
	due := tok.Cookie != "" && tok.Expires.Sub(now) < tokenWarning && now.Sub(c.tokenAlerted) >= 24*time.Hour
	if due {
		c.tokenAlerted = now
	}
	c.mu.Unlock()
	if due {
		alert(AlertCookieExpiring, tokenMessage("共享 Cookie", tok, now))
	}
}
