package fetcher

import (
	"encoding/base64"
	"testing"
)

func TestTokenFromCookie(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"name":"x","exp":1800332478}`))
	tok, ok := tokenFromCookie("other=1; AUTH_TOKEN=eyJhbGciOiJIUzI1NiJ9." + payload + ".sig")
	if !ok || tok.Cookie != "AUTH_TOKEN" || tok.Expires.Unix() != 1800332478 {
		t.Fatalf("got %+v %v", tok, ok)
	}
	for _, c := range []string{"", "sid=abc", "a=b.c.d"} {
		if _, ok := tokenFromCookie(c); ok {
			t.Fatalf("%q: unexpected token", c)
		}
	}
}
