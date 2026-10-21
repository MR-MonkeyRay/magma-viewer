package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
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

// token format: <expiry-unix>.<hmac(username|expiry)>
func (a *auth) newToken() (string, time.Time) {
	exp := a.now().Add(a.ttl)
	e := strconv.FormatInt(exp.Unix(), 10)
	return e + "." + a.sign(a.username+"|"+e), exp
}

func (a *auth) validToken(tok string) bool {
	e, sig, ok := strings.Cut(tok, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(e, 10, 64)
	if err != nil || a.now().Unix() >= exp {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(a.sign(a.username+"|"+e)))
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

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// allowAttempt reports whether ip is still under the failed-login limit.
func (a *auth) allowAttempt(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	f, ok := a.failures[ip]
	if !ok {
		return true
	}
	if a.now().Sub(f.since) > loginWindow {
		delete(a.failures, ip)
		return true
	}
	return f.count < loginMaxFailures
}

func (a *auth) recordFailure(ip string) {
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
	f.count++
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
