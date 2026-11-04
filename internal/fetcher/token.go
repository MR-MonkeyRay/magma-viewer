package fetcher

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"time"
)

// TokenInfo describes the JWT in the active cookie, so its expiry can be
// surfaced before fetches fail.
type TokenInfo struct {
	Cookie  string    `json:"cookie"`
	Expires time.Time `json:"expires"`
}

// tokenFromCookie returns the first cookie value that is a JWT with an exp claim.
func tokenFromCookie(cookie string) (TokenInfo, bool) {
	cookies, err := http.ParseCookie(cookie)
	if err != nil {
		return TokenInfo{}, false
	}
	for _, c := range cookies {
		parts := strings.Split(c.Value, ".")
		if len(parts) != 3 {
			continue
		}
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			continue
		}
		var claims struct {
			Exp int64 `json:"exp"`
		}
		if json.Unmarshal(payload, &claims) != nil || claims.Exp == 0 {
			continue
		}
		return TokenInfo{Cookie: c.Name, Expires: time.Unix(claims.Exp, 0)}, true
	}
	return TokenInfo{}, false
}

// Token returns the effective cookie token, or a zero value if none was found.
func (f *Fetcher) Token() TokenInfo {
	cookie, _ := f.effectiveCookie()
	tok, _ := tokenFromCookie(cookie)
	return tok
}

// tokenWarning is how early an expiring cookie is flagged.
const tokenWarning = 7 * 24 * time.Hour

func logToken(tok TokenInfo, now time.Time) {
	if tok.Cookie == "" {
		return
	}
	switch left := tok.Expires.Sub(now); {
	case left <= 0:
		log.Printf("fetcher: WARNING cookie %s expired at %s, update the cookie", tok.Cookie, tok.Expires.Format(time.DateTime))
	case left < tokenWarning:
		log.Printf("fetcher: WARNING cookie %s expires at %s", tok.Cookie, tok.Expires.Format(time.DateTime))
	default:
		log.Printf("fetcher: cookie %s valid until %s", tok.Cookie, tok.Expires.Format(time.DateTime))
	}
}

func tokenMessage(label string, tok TokenInfo, now time.Time) string {
	if left := tok.Expires.Sub(now); left <= 0 {
		return fmt.Sprintf("%s %s 已于 %s 过期，请在管理面板更新。", label, tok.Cookie, tok.Expires.Format(time.DateTime))
	} else {
		return fmt.Sprintf("%s %s 将在 %s 过期（约 %d 天后），请及时更新。", label, tok.Cookie, tok.Expires.Format(time.DateTime), int(math.Ceil(left.Hours()/24)))
	}
}

// WatchToken alerts once a day for a board override nearing expiry.
// Shared credentials are watched by Credentials.WatchToken instead.
func (f *Fetcher) WatchToken(stop <-chan struct{}, alert func(event, message string)) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		f.checkToken(alert)
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

func (f *Fetcher) checkToken(alert func(event, message string)) {
	if alert == nil {
		return
	}
	now := f.now()
	f.statusMu.Lock()
	tok, _ := tokenFromCookie(f.boardCookie)
	due := tok.Cookie != "" && tok.Expires.Sub(now) < tokenWarning && now.Sub(f.tokenAlerted) >= 24*time.Hour
	if due {
		f.tokenAlerted = now
	}
	f.statusMu.Unlock()
	if due {
		alert(AlertCookieExpiring, tokenMessage("Cookie", tok, now))
	}
}
