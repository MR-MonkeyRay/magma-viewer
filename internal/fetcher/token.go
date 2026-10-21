package fetcher

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// TokenInfo describes a JWT found in the configured cookie (magma's
// AUTH_TOKEN), so expiry can be surfaced before fetches start failing.
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

// Token returns the cookie token's expiry, or nil when none was found.
func (f *Fetcher) Token() *TokenInfo {
	if f.token.Cookie == "" {
		return nil
	}
	t := f.token
	return &t
}
