package web

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"magma-viewer/internal/config"
	"magma-viewer/internal/fetcher"
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

func newTestServer(t *testing.T, n int) (*httptest.Server, string) {
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
	srv := httptest.NewServer(New(cfg, st, f, static).Handler())
	t.Cleanup(srv.Close)

	q := url.Values{}
	for _, n := range names {
		q.Add("f", n)
	}
	return srv, q.Encode()
}

func login(t *testing.T, base string) *http.Cookie {
	t.Helper()
	resp, err := http.Post(base+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"pw"}`))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %v %v", err, resp.Status)
	}
	return resp.Cookies()[0]
}

func get(t *testing.T, u string, c *http.Cookie) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, u, nil)
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
