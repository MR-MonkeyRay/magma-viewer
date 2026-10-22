package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"magma-viewer/internal/config"
	"magma-viewer/internal/fetcher"
	"magma-viewer/internal/notice"
	"magma-viewer/internal/store"
)

func TestToken(t *testing.T) {
	a := newAuth("admin", "pw", "secret", time.Hour)
	tok, _ := a.newToken()
	if !a.validToken(tok) {
		t.Fatal("fresh token rejected")
	}
	if a.validToken(tok + "x") {
		t.Fatal("tampered token accepted")
	}
	if newAuth("admin", "pw", "other", time.Hour).validToken(tok) {
		t.Fatal("token accepted with another secret")
	}
	a.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if a.validToken(tok) {
		t.Fatal("expired token accepted")
	}
}

func openNotices(t *testing.T) *notice.Store {
	t.Helper()
	ns, err := notice.Open(filepath.Join(t.TempDir(), "notices.json"))
	if err != nil {
		t.Fatal(err)
	}
	return ns
}

func newTestServer(t *testing.T, n int) (*httptest.Server, string) {
	t.Helper()
	srv, q, _ := newTestServerStore(t, n)
	return srv, q
}

// newTestServerStore serves n tiny images and returns the server, an f=... query naming them all, and the store.
func newTestServerStore(t *testing.T, n int) (*httptest.Server, string, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	var names []string
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("20261020-1200%02d_%032d.png", i, i)
		names = append(names, name)
		os.WriteFile(filepath.Join(dir, name), []byte("img"), 0o644)
	}
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Admin:    config.Admin{Username: "admin", Password: "pw", SessionTTL: time.Hour},
		Magma:    config.Magma{URL: "http://127.0.0.1:1", Timeout: time.Second},
		Schedule: "* * * * *",
	}
	f := fetcher.New(cfg.Magma, t.TempDir(), st)
	static := fstest.MapFS{"index.html": {Data: []byte("<html></html>")}}
	srv := httptest.NewServer(New(cfg, st, f, openNotices(t), static).Handler())
	t.Cleanup(srv.Close)

	q := url.Values{}
	for _, n := range names {
		q.Add("f", n)
	}
	return srv, q.Encode(), st
}

func login(t *testing.T, base string) *http.Cookie {
	t.Helper()
	resp, err := http.Post(base+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"pw"}`))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %v %v", err, resp.Status)
	}
	return resp.Cookies()[0]
}

// get fetches u like a browser on the site's own page would.
func get(t *testing.T, u string, c *http.Cookie) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if c != nil {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func zipCount(t *testing.T, resp *http.Response) int {
	t.Helper()
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return len(zr.File)
}

func TestDownloadLimits(t *testing.T) {
	srv, all11 := newTestServer(t, 11)

	if r := get(t, srv.URL+"/download/zip?"+all11, nil); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("guest 11: %s", r.Status)
	}
	ten := all11[:strings.LastIndex(all11, "&")]
	if r := get(t, srv.URL+"/download/zip?"+ten, nil); r.StatusCode != http.StatusOK || zipCount(t, r) != 10 {
		t.Fatalf("guest 10: %s", r.Status)
	}
	if r := get(t, srv.URL+"/download/all", nil); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("guest all: %s", r.Status)
	}
	if r := get(t, srv.URL+"/download/zip?f=../../etc/passwd", nil); r.StatusCode != http.StatusNotFound {
		t.Fatalf("traversal: %s", r.Status)
	}

	c := login(t, srv.URL)
	if r := get(t, srv.URL+"/download/zip?"+all11, c); r.StatusCode != http.StatusOK || zipCount(t, r) != 11 {
		t.Fatalf("admin 11: %s", r.Status)
	}
	if r := get(t, srv.URL+"/download/all", c); r.StatusCode != http.StatusOK || zipCount(t, r) != 11 {
		t.Fatalf("admin all: %s", r.Status)
	}

	r := get(t, srv.URL+"/download/20261020-120000_"+strings.Repeat("0", 32)+".png", nil)
	if r.StatusCode != http.StatusOK || !strings.HasPrefix(r.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("single: %s %s", r.Status, r.Header.Get("Content-Disposition"))
	}
}

func TestLoginRejectsBadPassword(t *testing.T) {
	srv, _ := newTestServer(t, 0)
	resp, _ := http.Post(srv.URL+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"bad"}`))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got %s", resp.Status)
	}
}

func TestSchedulePause(t *testing.T) {
	srv, _ := newTestServer(t, 0)
	postAs := func(ct, body string, c *http.Cookie) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/schedule", strings.NewReader(body))
		req.Header.Set("Content-Type", ct)
		if c != nil {
			req.AddCookie(c)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	post := func(body string, c *http.Cookie) *http.Response { return postAs("application/json", body, c) }
	if r := post(`{"paused":true}`, nil); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("guest: %s", r.Status)
	}
	c := login(t, srv.URL)
	if r := postAs("text/plain", `{"paused":true}`, c); r.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("form-style post: %s", r.Status)
	}
	if r := post(`{}`, c); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing field: %s", r.Status)
	}
	if r := post(`{"paused":true}`, c); r.StatusCode != http.StatusOK {
		t.Fatalf("admin: %s", r.Status)
	}

	var body struct {
		Task struct {
			Paused bool `json:"paused"`
		} `json:"task"`
	}
	r := get(t, srv.URL+"/api/images", nil)
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Task.Paused {
		t.Fatal("guest list does not report paused task")
	}
}

func TestListReportsNextRunAndVersion(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Magma: config.Magma{URL: "http://127.0.0.1:1", Timeout: time.Second}, Schedule: "* * * * *"}
	s := New(cfg, st, fetcher.New(cfg.Magma, t.TempDir(), st), openNotices(t), fstest.MapFS{})
	next := time.Date(2026, 10, 21, 12, 1, 0, 0, time.UTC)
	s.NextRun = func() time.Time { return next }
	s.Version = "1.2.3"
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	var body struct {
		Task struct {
			NextRun time.Time `json:"next_run"`
		} `json:"task"`
		Version string `json:"version"`
	}
	r := get(t, srv.URL+"/api/images", nil)
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Task.NextRun.Equal(next) {
		t.Fatalf("next_run = %v, want %v", body.Task.NextRun, next)
	}
	if body.Version != "1.2.3" {
		t.Fatalf("version = %q", body.Version)
	}
}

func TestAPIResponsesAreNotCached(t *testing.T) {
	srv, q := newTestServer(t, 2)
	for _, path := range []string{"/api/images", "/api/overview", "/api/status", "/download/zip?" + q} {
		r := get(t, srv.URL+path, nil)
		r.Body.Close()
		if cc := r.Header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s: Cache-Control = %q", path, cc)
		}
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		remote, header, want string
	}{
		{"203.0.113.9:4000", "198.51.100.7", "203.0.113.9"}, // public peer cannot claim another IP
		{"127.0.0.1:4000", "198.51.100.7", "198.51.100.7"},
		{"172.18.0.1:4000", " 2001:db8::1 ", "2001:db8::1"}, // docker bridge gateway
		{"[::1]:4000", "198.51.100.7", "198.51.100.7"},
		{"127.0.0.1:4000", "not-an-ip", "127.0.0.1"},
		{"127.0.0.1:4000", "", "127.0.0.1"},
	}
	a := newAuth("admin", "pw", "", time.Hour)
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, "/api/login", nil)
		r.RemoteAddr = c.remote
		if c.header != "" {
			r.Header.Set("X-Real-IP", c.header)
		}
		if got := a.clientIP(r); got != c.want {
			t.Errorf("clientIP(%s, %q) = %s, want %s", c.remote, c.header, got, c.want)
		}
	}

	// With trusted_proxies set, only those peers may speak for a client.
	a.proxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	for remote, want := range map[string]string{"127.0.0.1:1": "198.51.100.7", "172.18.0.1:1": "172.18.0.1", "[::ffff:127.0.0.1]:1": "198.51.100.7"} {
		r := httptest.NewRequest(http.MethodPost, "/api/login", nil)
		r.RemoteAddr = remote
		r.Header.Set("X-Real-IP", "198.51.100.7")
		if got := a.clientIP(r); got != want {
			t.Errorf("trusted 127.0.0.1: clientIP(%s) = %s, want %s", remote, got, want)
		}
	}
}

func TestLoginLimitHoldsUnderConcurrency(t *testing.T) {
	srv, _ := newTestServer(t, 0)
	var wg sync.WaitGroup
	var unauthorized, limited atomic.Int32
	for range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Post(srv.URL+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"bad"}`))
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
			switch resp.StatusCode {
			case http.StatusUnauthorized:
				unauthorized.Add(1)
			case http.StatusTooManyRequests:
				limited.Add(1)
			}
		}()
	}
	wg.Wait()
	if unauthorized.Load() != loginMaxFailures || limited.Load() != 40-loginMaxFailures {
		t.Fatalf("401=%d 429=%d, want %d guesses checked", unauthorized.Load(), limited.Load(), loginMaxFailures)
	}
}

func TestPasswordChangeEndsSessions(t *testing.T) {
	tok, _ := newAuth("admin", "old", "secret", time.Hour).newToken()
	if newAuth("admin", "new", "secret", time.Hour).validToken(tok) {
		t.Fatal("session survived a password change")
	}
}

func TestZipSkipsFileDeletedBehindIndex(t *testing.T) {
	srv, all, st := newTestServerStore(t, 3)
	gone := st.All()[0].Name
	os.Remove(filepath.Join(st.Dir(), gone))

	c := login(t, srv.URL)
	if r := get(t, srv.URL+"/download/zip?"+all, c); r.StatusCode != http.StatusOK || zipCount(t, r) != 2 {
		t.Fatalf("zip without deleted file: %s", r.Status)
	}
	r := get(t, srv.URL+"/img/"+gone, nil)
	r.Body.Close()
	if r.StatusCode != http.StatusNotFound || r.Header.Get("Cache-Control") != "" {
		t.Fatalf("deleted image: %s, Cache-Control %q", r.Status, r.Header.Get("Cache-Control"))
	}
}

func TestCrossSiteRequestsRejected(t *testing.T) {
	srv, _ := newTestServer(t, 1)
	c := login(t, srv.URL)
	send := func(method, path, site string, extra ...string) int {
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		req.AddCookie(c)
		if site != "" {
			req.Header.Set("Sec-Fetch-Site", site)
		}
		for i := 0; i+1 < len(extra); i += 2 {
			req.Header.Set(extra[i], extra[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := send(http.MethodPost, "/api/schedule", "cross-site"); got != http.StatusForbidden {
		t.Errorf("cross-site POST: %d", got)
	}
	if got := send(http.MethodGet, "/download/all", "cross-site"); got != http.StatusForbidden {
		t.Errorf("cross-site download all: %d", got)
	}
	if got := send(http.MethodGet, "/download/all", "same-origin"); got != http.StatusOK {
		t.Errorf("same-origin download all: %d", got)
	}
	// Plain-http origins get no Fetch Metadata: Origin or Referer must prove the request came from here.
	if got := send(http.MethodGet, "/download/all", ""); got != http.StatusForbidden {
		t.Errorf("download all without any origin evidence: %d", got)
	}
	if got := send(http.MethodGet, "/download/all", "", "Referer", "http://evil.example/page"); got != http.StatusForbidden {
		t.Errorf("download all from another site's link: %d", got)
	}
	if got := send(http.MethodGet, "/download/all", "", "Referer", srv.URL+"/"); got != http.StatusOK {
		t.Errorf("download all from our page over http: %d", got)
	}
	if got := send(http.MethodPost, "/api/logout", "same-origin"); got != http.StatusOK {
		t.Errorf("same-origin logout: %d", got)
	}
}

func TestSecurityHeaders(t *testing.T) {
	srv, _ := newTestServer(t, 0)
	r := get(t, srv.URL+"/", nil)
	r.Body.Close()
	for _, h := range []string{"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "Content-Security-Policy"} {
		if r.Header.Get(h) == "" {
			t.Errorf("missing %s", h)
		}
	}
}

func TestOverviewHonoursTimeZone(t *testing.T) {
	srv, _ := newTestServer(t, 1) // saved 2026-10-20 12:00:00 server time
	day := func(tz string) string {
		var body struct {
			Days []struct {
				Date string `json:"date"`
			} `json:"days"`
		}
		r := get(t, srv.URL+"/api/overview?tz="+url.QueryEscape(tz), nil)
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Days) != 1 {
			t.Fatalf("tz %q: %v %v", tz, err, body)
		}
		return body.Days[0].Date
	}
	// 12:00 server time falls on the previous or next day somewhere 13 hours away.
	local := time.Date(2026, 10, 20, 12, 0, 0, 0, time.Local)
	_, off := local.Zone()
	far := off/3600 - 13
	if far < -12 {
		far += 26
	}
	tz := fmt.Sprintf("Etc/GMT%+d", -far) // Etc/GMT signs are inverted
	if got, want := day(tz), local.In(time.FixedZone("", far*3600)).Format("2006-01-02"); got != want || got == "2026-10-20" {
		t.Errorf("%s: %s, want %s", tz, got, want)
	}
	if got, want := day("No/Such_Zone"), local.Format("2006-01-02"); got != want {
		t.Errorf("unknown zone: %s, want server time %s", got, want)
	}
}

func TestNotices(t *testing.T) {
	srv, _ := newTestServer(t, 0)
	send := func(method, path, body string, c *http.Cookie) (*http.Response, map[string]json.RawMessage) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if c != nil {
			req.AddCookie(c)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]json.RawMessage
		json.NewDecoder(resp.Body).Decode(&out)
		return resp, out
	}
	public := func() []notice.Notice {
		_, out := send(http.MethodGet, "/api/notices", "", nil)
		var items []notice.Notice
		json.Unmarshal(out["items"], &items)
		return items
	}

	if r, _ := send(http.MethodPost, "/api/notices", `{"title":"x","publish":true}`, nil); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("guest create: %s", r.Status)
	}
	if r, _ := send(http.MethodGet, "/api/notices/all", "", nil); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("guest list all: %s", r.Status)
	}
	c := login(t, srv.URL)
	if _, out := send(http.MethodGet, "/api/notices/all", "", c); string(out["items"]) != "[]" {
		t.Fatalf("empty list encodes as %s", out["items"])
	}
	if r, out := send(http.MethodPost, "/api/notices", `{"title":"  "}`, c); r.StatusCode != http.StatusBadRequest || string(out["code"]) != `"title_required"` {
		t.Fatalf("blank title: %s %s %s", r.Status, out["code"], out["error"])
	}

	r, out := send(http.MethodPost, "/api/notices", `{"title":"维护","body":"周六"}`, c)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("create draft: %s", r.Status)
	}
	var n notice.Notice
	json.Unmarshal(out["notice"], &n)
	if n.IsPublished() || len(public()) != 0 {
		t.Fatal("draft is public")
	}

	if r, _ := send(http.MethodPut, "/api/notices/"+n.ID, `{"title":"维护","body":"周六","publish":true}`, c); r.StatusCode != http.StatusOK {
		t.Fatalf("publish: %s", r.Status)
	}
	got := public()
	if len(got) != 1 || got[0].Title != "维护" {
		t.Fatalf("public after publish: %+v", got)
	}
	var list struct {
		Notice time.Time `json:"notice"`
	}
	json.NewDecoder(get(t, srv.URL+"/api/images", nil).Body).Decode(&list)
	if !list.Notice.Equal(got[0].Published) {
		t.Fatalf("list reports notice %v, want %v", list.Notice, got[0].Published)
	}

	if r, _ := send(http.MethodPut, "/api/notices/missing", `{"title":"x"}`, c); r.StatusCode != http.StatusNotFound {
		t.Fatalf("update missing: %s", r.Status)
	}
	if r, _ := send(http.MethodDelete, "/api/notices/"+n.ID, "", c); r.StatusCode != http.StatusOK || len(public()) != 0 {
		t.Fatalf("delete: %s", r.Status)
	}
}
