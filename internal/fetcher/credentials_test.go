package fetcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCredentialsSetClearAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".state", "cookie.json")
	c := NewCredentials("sid=config", path)
	if cookie, info := c.Cookie(); cookie != "sid=config" || info.Source != "config" || !info.Updated.IsZero() {
		t.Fatalf("initial Cookie = %q, %+v", cookie, info)
	}

	updated := time.Date(2026, 11, 2, 12, 34, 56, 789, time.UTC)
	c.now = func() time.Time { return updated }
	if err := c.Set("Cookie: sid=page"); err != nil {
		t.Fatal(err)
	}
	if cookie, info := c.Cookie(); cookie != "sid=page" || info.Source != "page" || !info.Updated.Equal(updated) {
		t.Fatalf("after Set Cookie = %q, %+v", cookie, info)
	}
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0o600 {
		t.Fatalf("cookie file mode = %v", stat.Mode().Perm())
	}

	restarted := NewCredentials("sid=config", path)
	if cookie, info := restarted.Cookie(); cookie != "sid=page" || info.Source != "page" || !info.Updated.Equal(updated) {
		t.Fatalf("after restart Cookie = %q, %+v", cookie, info)
	}
	if err := restarted.Clear(); err != nil {
		t.Fatal(err)
	}
	if cookie, info := restarted.Cookie(); cookie != "sid=config" || info.Source != "config" || !info.Updated.IsZero() {
		t.Fatalf("after Clear Cookie = %q, %+v", cookie, info)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cookie file remains: %v", err)
	}
	if err := restarted.Clear(); err != nil {
		t.Fatalf("second Clear = %v", err)
	}
}

func TestCredentialsConfigFingerprintInvalidatesOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".state", "cookie.json")
	c := NewCredentials("sid=old-config", path)
	if err := c.Set("sid=page"); err != nil {
		t.Fatal(err)
	}

	c = NewCredentials("sid=new-config", path)
	if cookie, info := c.Cookie(); cookie != "sid=new-config" || info.Source != "config" {
		t.Fatalf("Cookie = %q, %+v", cookie, info)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale override remains: %v", err)
	}
}

func TestCredentialsRejectsInvalidCookieWithoutChangingState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookie.json")
	c := NewCredentials("sid=config", path)
	for _, raw := range []string{"", " ", "sid=", "sid=x\ny"} {
		if err := c.Set(raw); err == nil {
			t.Errorf("Set(%q) succeeded", raw)
		}
	}
	if cookie, info := c.Cookie(); cookie != "sid=config" || info.Source != "config" {
		t.Fatalf("Cookie = %q, %+v", cookie, info)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid Set created file: %v", err)
	}
}

func TestCredentialsConcurrentAccess(t *testing.T) {
	c := NewCredentials("sid=config", filepath.Join(t.TempDir(), "cookie.json"))
	var wg sync.WaitGroup
	for i := range 12 {
		i := i
		wg.Go(func() {
			if err := c.Set(fmt.Sprintf("sid=page-%d", i)); err != nil {
				t.Errorf("Set: %v", err)
			}
			cookie, info := c.Cookie()
			if cookie == "" || (info.Source != "page" && info.Source != "config") {
				t.Errorf("Cookie = %q, %+v", cookie, info)
			}
			_ = c.Token()
		})
	}
	wg.Wait()
	if cookie, info := c.Cookie(); !strings.HasPrefix(cookie, "sid=page-") || info.Source != "page" {
		t.Fatalf("final Cookie = %q, %+v", cookie, info)
	}
}

func TestCredentialsTokenAndDailySharedReminder(t *testing.T) {
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	c := NewCredentials("AUTH_TOKEN="+jwt(now.Add(48*time.Hour)), filepath.Join(t.TempDir(), "cookie.json"))
	c.now = func() time.Time { return now }
	if tok := c.Token(); tok.Cookie != "AUTH_TOKEN" || !tok.Expires.Equal(now.Add(48*time.Hour)) {
		t.Fatalf("Token = %+v", tok)
	}

	var events, messages []string
	alert := func(event, message string) {
		events = append(events, event)
		messages = append(messages, message)
	}
	c.checkToken(alert)
	c.checkToken(alert)
	if len(events) != 1 || events[0] != AlertCookieExpiring || !strings.Contains(messages[0], "共享 Cookie") {
		t.Fatalf("alerts = %v, messages = %v", events, messages)
	}
	now = now.Add(23 * time.Hour)
	c.checkToken(alert)
	if len(events) != 1 {
		t.Fatalf("reminded again before a day: %v", events)
	}
	now = now.Add(time.Hour)
	c.checkToken(alert)
	if len(events) != 2 {
		t.Fatalf("daily reminder missing: %v", events)
	}

	if err := c.Set("AUTH_TOKEN=" + jwt(now.Add(48*time.Hour))); err != nil {
		t.Fatal(err)
	}
	c.checkToken(alert)
	if len(events) != 3 {
		t.Fatalf("Set did not reset reminder state: %v", events)
	}

	if err := c.Set("AUTH_TOKEN=" + jwt(now.Add(48*time.Hour))); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	watched := make(chan string, 1)
	go c.WatchToken(stop, func(_ string, message string) { watched <- message })
	select {
	case message := <-watched:
		close(stop)
		if !strings.Contains(message, "共享 Cookie") {
			t.Fatalf("WatchToken message = %q", message)
		}
	case <-time.After(time.Second):
		close(stop)
		t.Fatal("WatchToken did not check immediately")
	}
}
