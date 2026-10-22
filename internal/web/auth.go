package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

const sessionCookie = "mv_session"

const (
	loginMaxFailures = 10
	loginWindow      = 5 * time.Minute
)

type auth struct {
	username string
	password string
	secret   []byte
	ttl      time.Duration
	now      func() time.Time
	proxies  []netip.Prefix // peers whose X-Real-IP is believed; nil means loopback and private ranges

	mu       sync.Mutex
	failures map[string]*failure
}

type failure struct {
	count int
	since time.Time
}

func newAuth(username, password, secret string, ttl time.Duration) *auth {
	key := []byte(secret)
	if secret == "" {
		key = make([]byte, 32)
		_, _ = rand.Read(key)
	}
	return &auth{
		username: username,
		password: password,
		secret:   key,
		ttl:      ttl,
		now:      time.Now,
		failures: map[string]*failure{},
	}
}

func (a *auth) sign(payload string) string {
	m := hmac.New(sha256.New, a.secret)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// token format: <expiry-unix>.<hmac(username|password|expiry)>; signing the
// password means changing it in the config ends every existing session.
func (a *auth) newToken() (string, time.Time) {
	exp := a.now().Add(a.ttl)
	e := strconv.FormatInt(exp.Unix(), 10)
	return e + "." + a.sign(a.payload(e)), exp
}

func (a *auth) payload(exp string) string { return a.username + "|" + a.password + "|" + exp }

func (a *auth) validToken(tok string) bool {
	e, sig, ok := strings.Cut(tok, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(e, 10, 64)
	if err != nil || a.now().Unix() >= exp {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(a.sign(a.payload(e))))
}

func (a *auth) isAdmin(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	return err == nil && a.validToken(c.Value)
}

func (a *auth) checkCredentials(user, pass string) bool {
	u := subtle.ConstantTimeCompare([]byte(user), []byte(a.username))
	p := subtle.ConstantTimeCompare([]byte(pass), []byte(a.password))
	return u&p == 1
}

// clientIP is the TCP peer, or X-Real-IP when the peer is a trusted reverse
// proxy (nginx overwrites any value the client sent).
func (a *auth) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if peer, err := netip.ParseAddr(host); err == nil && a.trusted(peer.Unmap()) {
		if real, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
			return real.Unmap().String()
		}
	}
	return host
}

func (a *auth) trusted(peer netip.Addr) bool {
	if a.proxies == nil {
		return peer.IsLoopback() || peer.IsPrivate()
	}
	for _, p := range a.proxies {
		if p.Contains(peer) {
			return true
		}
	}
	return false
}

// beginAttempt counts a login attempt from ip and reports whether it is still
// under the limit. Counting before the password is checked keeps concurrent
// guesses from all passing the check; a successful login clears the count.
func (a *auth) beginAttempt(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	// Drop stale entries so the map cannot grow without bound.
	for k, f := range a.failures {
		if now.Sub(f.since) > loginWindow {
			delete(a.failures, k)
		}
	}
	f, ok := a.failures[ip]
	if !ok {
		f = &failure{since: now}
		a.failures[ip] = f
	}
	if f.count >= loginMaxFailures {
		return false
	}
	f.count++
	return true
}

func (a *auth) clearFailures(ip string) {
	a.mu.Lock()
	delete(a.failures, ip)
	a.mu.Unlock()
}

func (a *auth) setSession(w http.ResponseWriter, r *http.Request) {
	tok, exp := a.newToken()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    tok,
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}
