package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"magma-viewer/internal/board"
	"magma-viewer/internal/config"
	"magma-viewer/internal/fetcher"
)

func createBoard(t *testing.T, boards *board.Manager, name string, hidden bool) board.Summary {
	t.Helper()
	s, err := boards.Create(board.Settings{Name: name, URL: "http://example.invalid/" + url.PathEscape(name), Hidden: hidden})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBoardSelectionAndLegacyCompatibility(t *testing.T) {
	srv, query, legacy := newTestServerStore(t, 1)
	name := legacy.All()[0].Name
	for _, path := range []string{"/api/images", "/img/" + name, "/download/zip?" + query} {
		r := get(t, srv.URL+path, nil)
		if r.StatusCode != http.StatusOK {
			t.Errorf("legacy %s: %s", path, r.Status)
		}
		r.Body.Close()
	}

	cfg := &config.Config{Admin: config.Admin{Username: "admin", Password: "pw", SessionTTL: time.Hour}}
	s, boards, _ := newTestWebServer(t, cfg, false)
	a := createBoard(t, boards, "A", false)
	b := createBoard(t, boards, "B", false)
	const shared = "same.png"
	for _, tc := range []struct{ id, body string }{{a.ID, "one"}, {b.ID, "two"}} {
		rt, _, _ := boards.Resolve(tc.id)
		if err := rt.Store.Import(strings.NewReader(tc.body), shared); err != nil {
			t.Fatal(err)
		}
	}
	h := s.Handler()
	for _, tc := range []struct{ id, body string }{{a.ID, "one"}, {b.ID, "two"}} {
		form := url.Values{"f": {shared}}.Encode()
		req := httptest.NewRequest(http.MethodPost, "/download/zip?board="+tc.id, strings.NewReader(form))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("board %s POST zip: %d", tc.id, rec.Code)
		}
		data := rec.Body.Bytes()
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil || len(zr.File) != 1 {
			t.Fatalf("board %s POST zip: %v files=%d", tc.id, err, len(zr.File))
		}
		f, err := zr.File[0].Open()
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(f)
		f.Close()
		if string(got) != tc.body {
			t.Fatalf("board %s POST zip body %q, want %q", tc.id, got, tc.body)
		}
	}
	for _, tc := range []struct{ id, body string }{{a.ID, "one"}, {b.ID, "two"}} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/img/"+shared+"?board="+tc.id, nil))
		if rec.Code != http.StatusOK || rec.Body.String() != tc.body {
			t.Errorf("board %s: status %d body %q", tc.id, rec.Code, rec.Body.String())
		}
	}
	for _, target := range []string{"/api/images?board=missing", "/img/" + shared + "?board=missing", "/img/" + shared} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d", target, rec.Code)
		}
	}
}

func TestOverviewCacheIsPerBoard(t *testing.T) {
	s, boards, _ := newTestWebServer(t, &config.Config{}, false)
	a := createBoard(t, boards, "A", false)
	b := createBoard(t, boards, "B", false)
	for i, id := range []string{a.ID, b.ID} {
		rt, _, _ := boards.Resolve(id)
		for n := 0; n <= i; n++ {
			name := fmt.Sprintf("20261020-12000%d_%032d.png", n, i*10+n)
			if err := os.WriteFile(filepath.Join(rt.Store.Dir(), name), []byte("img"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		rt.Store.Rescan()
	}
	rtA, _, _ := boards.Resolve(a.ID)
	rtB, _, _ := boards.Resolve(b.ID)
	if rtA.Store.Generation() != rtB.Store.Generation() {
		t.Fatalf("test requires equal generations, got %d and %d", rtA.Store.Generation(), rtB.Store.Generation())
	}
	h := s.Handler()
	for _, tc := range []struct {
		id   string
		want float64
	}{{a.ID, 1}, {b.ID, 2}, {a.ID, 1}} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/overview?tz=UTC&board="+tc.id, nil))
		var got map[string]any
		_ = json.NewDecoder(rec.Body).Decode(&got)
		if rec.Code != http.StatusOK || got["count"] != tc.want {
			t.Errorf("board %s: status %d body %v", tc.id, rec.Code, got)
		}
	}
}

func TestBoardsVisibilityAndAdminCRUD(t *testing.T) {
	cfg := &config.Config{Admin: config.Admin{Username: "admin", Password: "pw", SessionTTL: time.Hour}}
	s, boards, _ := newTestWebServer(t, cfg, false)
	visible := createBoard(t, boards, "Visible", false)
	_ = createBoard(t, boards, "Hidden", true)
	rt, _, _ := boards.Resolve(visible.ID)
	if err := rt.Store.Import(strings.NewReader("img"), "20260101-000000_"+strings.Repeat("a", 32)+".jpg"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	public := decode(t, get(t, srv.URL+"/api/boards", nil))
	if items := public["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != visible.ID || public["suggested"] != visible.ID {
		t.Fatalf("public boards: %v", public)
	}
	if r := get(t, srv.URL+"/api/admin/boards", nil); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("guest admin boards: %s", r.Status)
	}
	c := login(t, srv.URL)
	if admin := decode(t, get(t, srv.URL+"/api/boards", c)); len(admin["items"].([]any)) != 2 {
		t.Fatalf("admin boards: %v", admin)
	}

	r := sendJSON(t, http.MethodPost, srv.URL+"/api/admin/boards", `{"name":"New","url":"http://example.invalid/new"}`, c)
	created := decode(t, r)
	if r.StatusCode != http.StatusCreated || created["paused"] != true {
		t.Fatalf("create: %s %v", r.Status, created)
	}
	id := created["id"].(string)
	r = sendJSON(t, http.MethodPost, srv.URL+"/api/admin/boards", `{"name":"","url":"bad"}`, c)
	if body := decode(t, r); r.StatusCode != http.StatusBadRequest || body["code"] != "name_required" {
		t.Fatalf("invalid create: %s %v", r.Status, body)
	}
	r = sendJSON(t, http.MethodPost, srv.URL+"/api/admin/boards", `{"name":"x","url":"http://example.invalid","padding":"`+strings.Repeat("x", 17<<10)+`"}`, c)
	if body := decode(t, r); r.StatusCode != http.StatusBadRequest || body["code"] != "bad_request" {
		t.Fatalf("oversized create: %s %v", r.Status, body)
	}
	r = sendJSON(t, http.MethodPut, srv.URL+"/api/admin/boards/"+id, `{"name":"Renamed","url":"http://example.invalid/new"}`, c)
	if body := decode(t, r); r.StatusCode != http.StatusOK || body["name"] != "Renamed" {
		t.Fatalf("update: %s %v", r.Status, body)
	}
	r = sendJSON(t, http.MethodPut, srv.URL+"/api/admin/boards/missing", `{"name":"X","url":"http://example.invalid/x"}`, c)
	if body := decode(t, r); r.StatusCode != http.StatusNotFound || body["code"] != "board_not_found" {
		t.Fatalf("missing update: %s %v", r.Status, body)
	}
}

func TestBoardsExplicitGuestSelection(t *testing.T) {
	cfg := &config.Config{Admin: config.Admin{Username: "admin", Password: "pw", SessionTTL: time.Hour}}
	s, boards, _ := newTestWebServer(t, cfg, false)
	visible := createBoard(t, boards, "Visible", false)
	hidden := createBoard(t, boards, "Hidden", true)
	otherHidden := createBoard(t, boards, "Other Hidden", true)
	archived := createBoard(t, boards, "Archived", true)
	rt, _, err := boards.Resolve(hidden.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Store.Import(strings.NewReader("img"), "20260101-000000_"+strings.Repeat("a", 32)+".jpg"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	cookie := login(t, srv.URL)
	if err := boards.Archive(archived.ID); err != nil {
		t.Fatal(err)
	}
	if op := waitOperation(t, srv.URL, archived.ID, cookie); op["phase"] != "done" {
		t.Fatalf("archive: %v", op)
	}
	if b, ok := boards.Board(archived.ID); !ok || b.Lifecycle != board.Archived {
		t.Fatalf("board was not archived: %+v", b)
	}

	for _, tc := range []struct {
		name  string
		query string
		ids   []string
	}{
		{"no parameter", "", []string{visible.ID}},
		{"empty parameter", "?board=", []string{visible.ID}},
		{"visible", "?board=" + visible.ID, []string{visible.ID}},
		{"hidden", "?board=" + hidden.ID, []string{visible.ID, hidden.ID}},
		{"other hidden", "?board=" + otherHidden.ID, []string{visible.ID, otherHidden.ID}},
		{"unknown", "?board=missing", []string{visible.ID}},
		{"invalid", "?board=%2Finvalid", []string{visible.ID}},
		{"archived", "?board=" + archived.ID, []string{visible.ID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := get(t, srv.URL+"/api/boards"+tc.query, nil)
			body := decode(t, resp)
			if resp.StatusCode != http.StatusOK || body["suggested"] != visible.ID || body["legacy_id"] != boards.LegacyID() {
				t.Fatalf("boards: %s %v", resp.Status, body)
			}
			items := body["items"].([]any)
			if len(items) != len(tc.ids) {
				t.Fatalf("items: %v, want IDs %v", items, tc.ids)
			}
			for i, id := range tc.ids {
				item := items[i].(map[string]any)
				want, err := boards.Summary(id)
				if err != nil {
					t.Fatal(err)
				}
				if item["id"] != id || item["name"] != want.Name || item["count"] != float64(want.Count) || item["state"] != want.State {
					t.Fatalf("item: %v, want %+v", item, want)
				}
				fields := 4
				if !want.Latest.IsZero() {
					fields++
					if item["latest"] != want.Latest.Format(time.RFC3339Nano) {
						t.Fatalf("latest: %v, want %v", item["latest"], want.Latest)
					}
				}
				if len(item) != fields {
					t.Fatalf("unexpected guest fields: %v", item)
				}
			}
		})
	}
	admin := decode(t, get(t, srv.URL+"/api/boards?board="+hidden.ID, cookie))
	if len(admin["items"].([]any)) != 3 || admin["suggested"] != visible.ID {
		t.Fatalf("admin boards changed: %v", admin)
	}
}

func TestAdminBoardSyncTimeJSONContract(t *testing.T) {
	cfg := &config.Config{Admin: config.Admin{Username: "admin", Password: "pw", SessionTTL: time.Hour}}
	s, boards, _ := newTestWebServer(t, cfg, false)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	c := login(t, srv.URL)

	const (
		from  = "2026-11-01T01:02:03Z"
		until = "2026-12-01T04:05:06Z"
	)
	base := `"name":"Window","url":"http://example.invalid/window"`
	assertUnbounded := func(id string, body map[string]any) {
		t.Helper()
		if _, ok := body["sync_from"]; ok {
			t.Fatalf("response includes zero sync_from: %v", body)
		}
		if _, ok := body["sync_until"]; ok {
			t.Fatalf("response includes zero sync_until: %v", body)
		}
		b, ok := boards.Board(id)
		if !ok {
			t.Fatalf("board %s was not persisted", id)
		}
		if !b.SyncFrom.IsZero() || !b.SyncUntil.IsZero() {
			t.Fatalf("board %s boundaries = %v to %v, want unbounded", id, b.SyncFrom, b.SyncUntil)
		}
	}

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "omitted", body: `{` + base + `}`},
		{name: "null", body: `{` + base + `,"sync_from":null,"sync_until":null}`},
	} {
		t.Run("post_"+tc.name, func(t *testing.T) {
			r := sendJSON(t, http.MethodPost, srv.URL+"/api/admin/boards", tc.body, c)
			body := decode(t, r)
			if r.StatusCode != http.StatusCreated {
				t.Fatalf("create: %s %v", r.Status, body)
			}
			assertUnbounded(body["id"].(string), body)
		})
	}

	r := sendJSON(t, http.MethodPost, srv.URL+"/api/admin/boards",
		`{`+base+`,"sync_from":"2026-11-01T09:02:03+08:00","sync_until":"2026-12-01T12:05:06+08:00"}`, c)
	created := decode(t, r)
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("create bounded board: %s %v", r.Status, created)
	}
	id := created["id"].(string)
	if created["sync_from"] != from || created["sync_until"] != until {
		t.Fatalf("bounded response must normalize timestamps to UTC: %v", created)
	}

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "omitted", body: `{` + base + `}`},
		{name: "null", body: `{` + base + `,"sync_from":null,"sync_until":null}`},
	} {
		t.Run("put_"+tc.name, func(t *testing.T) {
			settings := board.Settings{
				Name:      "Window",
				URL:       "http://example.invalid/window",
				SyncFrom:  time.Date(2026, 11, 1, 1, 2, 3, 0, time.UTC),
				SyncUntil: time.Date(2026, 12, 1, 4, 5, 6, 0, time.UTC),
			}
			if _, err := boards.Update(id, settings); err != nil {
				t.Fatal(err)
			}
			r := sendJSON(t, http.MethodPut, srv.URL+"/api/admin/boards/"+id, tc.body, c)
			body := decode(t, r)
			if r.StatusCode != http.StatusOK {
				t.Fatalf("update: %s %v", r.Status, body)
			}
			assertUnbounded(id, body)
		})
	}

	for _, method := range []string{http.MethodPost, http.MethodPut} {
		for _, field := range []string{"sync_from", "sync_until"} {
			for _, value := range []string{`""`, `"not-a-time"`} {
				name := strings.ToLower(method) + "_" + field + "_" + strings.Trim(value, `"`)
				t.Run(name, func(t *testing.T) {
					settings := board.Settings{
						Name:      "Window",
						URL:       "http://example.invalid/window",
						SyncFrom:  time.Date(2026, 11, 1, 1, 2, 3, 0, time.UTC),
						SyncUntil: time.Date(2026, 12, 1, 4, 5, 6, 0, time.UTC),
					}
					if _, err := boards.Update(id, settings); err != nil {
						t.Fatal(err)
					}
					before := len(boards.All())
					target := srv.URL + "/api/admin/boards"
					if method == http.MethodPut {
						target += "/" + id
					}
					r := sendJSON(t, method, target, `{`+base+`,"`+field+`":`+value+`}`, c)
					body := decode(t, r)
					if r.StatusCode != http.StatusBadRequest || body["code"] != "bad_request" {
						t.Fatalf("invalid %s: %s %v", field, r.Status, body)
					}
					if got := len(boards.All()); got != before {
						t.Fatalf("invalid create changed board count from %d to %d", before, got)
					}
					b, ok := boards.Board(id)
					if !ok || b.SyncFrom.Format(time.RFC3339) != from || b.SyncUntil.Format(time.RFC3339) != until {
						t.Fatalf("invalid request changed existing boundaries: %+v", b.Settings)
					}
				})
			}
		}
	}
}

func TestBoardCookieInheritance(t *testing.T) {
	cfg := &config.Config{Admin: config.Admin{Username: "admin", Password: "pw", SessionTTL: time.Hour}}
	s, boards, _ := newTestWebServer(t, cfg, false)
	a := createBoard(t, boards, "A", false)
	b := createBoard(t, boards, "B", false)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	c := login(t, srv.URL)

	assertSource := func(id, want string) {
		t.Helper()
		rt, _, _ := boards.Resolve(id)
		if got := rt.Fetcher.Cookie().Source; got != want {
			t.Fatalf("board %s source %q, want %q", id, got, want)
		}
	}
	assertMatchesGet := func(path string, putBody []byte) {
		t.Helper()
		r := get(t, srv.URL+path, c)
		getBody, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil || r.StatusCode != http.StatusOK || !bytes.Equal(putBody, getBody) {
			t.Fatalf("%s: PUT body %s, GET %s body %s, error %v", path, putBody, r.Status, getBody, err)
		}
	}
	r := sendJSON(t, http.MethodPut, srv.URL+"/api/cookie", `{"cookie":"AUTH_TOKEN=shared-secret"}`, c)
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != http.StatusOK || bytes.Contains(body, []byte("shared-secret")) {
		t.Fatalf("shared cookie response: %s %s", r.Status, body)
	}
	assertMatchesGet("/api/cookie", body)
	assertSource(a.ID, "page")
	assertSource(b.ID, "page")
	r = sendJSON(t, http.MethodPut, srv.URL+"/api/cookie?board="+a.ID, `{"cookie":"AUTH_TOKEN=board-secret"}`, c)
	body, _ = io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != http.StatusOK || bytes.Contains(body, []byte("board-secret")) {
		t.Fatalf("board cookie response: %s %s", r.Status, body)
	}
	assertMatchesGet("/api/cookie?board="+a.ID, body)
	assertSource(a.ID, "board")
	assertSource(b.ID, "page")
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/cookie?board="+a.ID, nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(c)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("delete board cookie: %s", r.Status)
	}
	assertSource(a.ID, "page")
}

func TestInactiveBoardCookieWrites(t *testing.T) {
	s, boards, _ := newTestWebServer(t, &config.Config{Admin: config.Admin{Username: "admin", Password: "pw", SessionTTL: time.Hour}}, false)
	b := createBoard(t, boards, "Inactive Cookie", false)
	rt, _, err := boards.Resolve(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Fetcher.SetCookie("AUTH_TOKEN=original"); err != nil {
		t.Fatal(err)
	}
	cookiePath := filepath.Join(rt.Store.Dir(), ".state", "board-cookie.json")
	before, err := os.ReadFile(cookiePath)
	if err != nil {
		t.Fatal(err)
	}
	// Model retirement after the handler resolves the board runtime.
	rt.Fetcher.Active = func() bool { return false }
	h := s.Handler()
	token, _ := s.auth.newToken()
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/cookie?board="+b.ID, strings.NewReader(`{"cookie":"AUTH_TOKEN=replacement"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			var body map[string]string
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if rec.Code != http.StatusConflict || body["code"] != "board_inactive" || body["error"] != "画板当前不可抓取" {
				t.Fatalf("inactive cookie write: %d %v", rec.Code, body)
			}
			after, err := os.ReadFile(cookiePath)
			if err != nil || !bytes.Equal(before, after) || rt.Fetcher.Cookie().Source != "board" {
				t.Fatalf("inactive cookie write changed credentials: %s, %v", after, err)
			}
		})
	}
}

func TestAdminHistoryMergesBoards(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "failed", http.StatusBadGateway)
	}))
	defer source.Close()
	cfg := &config.Config{
		Admin: config.Admin{Username: "admin", Password: "pw", SessionTTL: time.Hour},
		Magma: config.Magma{Timeout: time.Second},
	}
	s, boards, _ := newTestWebServer(t, cfg, false)
	a := createBoard(t, boards, "A", false)
	b := createBoard(t, boards, "B", false)
	for _, summary := range []board.Summary{a, b} {
		settings := summary.Settings
		settings.URL = source.URL
		if _, err := boards.Update(summary.ID, settings); err != nil {
			t.Fatal(err)
		}
		rt, _, _ := boards.Resolve(summary.ID)
		_, _ = rt.Fetcher.Run(t.Context())
		time.Sleep(time.Millisecond)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	c := login(t, srv.URL)
	body := decode(t, get(t, srv.URL+"/api/admin/history", c))
	items, ok := body["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("history: %v", body)
	}
	seen := map[string]string{}
	var previous time.Time
	for _, raw := range items {
		item := raw.(map[string]any)
		seen[item["board_id"].(string)] = item["board_name"].(string)
		when, err := time.Parse(time.RFC3339Nano, item["time"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if !previous.IsZero() && when.After(previous) {
			t.Fatalf("history not newest first: %v", items)
		}
		previous = when
	}
	if seen[a.ID] != "A" || seen[b.ID] != "B" {
		t.Fatalf("history board attribution: %v", seen)
	}
	filtered := decode(t, get(t, srv.URL+"/api/admin/history?board="+a.ID, c))
	filteredItems, ok := filtered["items"].([]any)
	if !ok || len(filteredItems) != 1 || filteredItems[0].(map[string]any)["board_id"] != a.ID {
		t.Fatalf("filtered history: %v", filtered)
	}
	r := get(t, srv.URL+"/api/admin/history?board=missing", c)
	if body := decode(t, r); r.StatusCode != http.StatusNotFound || body["code"] != "board_not_found" {
		t.Fatalf("unknown history board: %s %v", r.Status, body)
	}
}

func TestAdminOverviewAggregatesBoards(t *testing.T) {
	cfg := &config.Config{Admin: config.Admin{Username: "admin", Password: "pw", SessionTTL: time.Hour}}
	s, boards, _ := newTestWebServer(t, cfg, false)
	for i, name := range []string{"A", "B"} {
		b := createBoard(t, boards, name, false)
		rt, _, _ := boards.Resolve(b.ID)
		if err := rt.Store.Import(strings.NewReader(strings.Repeat("x", i+1)), fmt.Sprintf("board-%d.jpg", i)); err != nil {
			t.Fatal(err)
		}
	}
	s.Version = "1.2.3"
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	c := login(t, srv.URL)
	body := decode(t, get(t, srv.URL+"/api/admin/overview", c))
	if body["version"] != "1.2.3" || body["schedule"] != cfg.Schedule || len(body["boards"].([]any)) != 2 {
		t.Fatalf("overview: %v", body)
	}
	disk := body["disk"].(map[string]any)
	if disk["archive"] != float64(3) {
		t.Fatalf("archive bytes: %v", disk)
	}
}

func TestBoardSuggestionAndFileDefaults(t *testing.T) {
	cfg := &config.Config{Admin: config.Admin{Username: "admin", Password: "pw", SessionTTL: time.Hour}}
	s, boards, _ := newTestWebServer(t, cfg, false)
	older := createBoard(t, boards, "Older", false)
	latest := createBoard(t, boards, "Latest", false)
	hidden := createBoard(t, boards, "Hidden", true)
	for i, b := range []board.Summary{older, latest, hidden} {
		rt, _, _ := boards.Resolve(b.ID)
		name := fmt.Sprintf("2026102%d-120000_%032d.jpg", i, i)
		if err := rt.Store.Import(strings.NewReader("img"), name); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	body := decode(t, get(t, srv.URL+"/api/boards", nil))
	if body["suggested"] != latest.ID {
		t.Fatalf("suggested should be the latest visible archive: %v", body)
	}
	for _, raw := range body["items"].([]any) {
		if _, ok := raw.(map[string]any)["hidden"]; ok {
			t.Fatalf("guest item includes hidden field: %v", raw)
		}
	}
	if body := decode(t, get(t, srv.URL+"/api/images", nil)); body["board"] != latest.ID {
		t.Fatalf("unqualified API should select suggested: %v", body)
	}
	c := login(t, srv.URL)
	for _, raw := range decode(t, get(t, srv.URL+"/api/boards", c))["items"].([]any) {
		if _, ok := raw.(map[string]any)["hidden"].(bool); !ok {
			t.Fatalf("admin item lacks hidden field: %v", raw)
		}
	}
	name := "20261021-120000_" + fmt.Sprintf("%032d", 1) + ".jpg"
	for _, target := range []string{"/img/" + name, "/thumb/" + name, "/preview/" + name, "/download/" + name, "/download/zip?f=" + name, "/download/all"} {
		r := get(t, srv.URL+target, c)
		r.Body.Close()
		if r.StatusCode != http.StatusNotFound {
			t.Errorf("unqualified file %s: %s", target, r.Status)
		}
	}
}

func TestCookieInfoAndSharedClear(t *testing.T) {
	cfg := &config.Config{Admin: config.Admin{Username: "admin", Password: "pw", SessionTTL: time.Hour}}
	s, boards, _ := newTestWebServer(t, cfg, false)
	b := createBoard(t, boards, "A", false)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	c := login(t, srv.URL)
	for _, target := range []string{"/api/cookie", "/api/cookie?board=" + b.ID} {
		r := get(t, srv.URL+target, nil)
		r.Body.Close()
		if r.StatusCode != http.StatusUnauthorized {
			t.Fatalf("guest cookie: %s", r.Status)
		}
		body := decode(t, get(t, srv.URL+target, c))
		if body["source"] != "none" || body["token"] != nil || body["override"] == true {
			t.Fatalf("cookie info: %v", body)
		}
	}
	r := sendJSON(t, http.MethodPut, srv.URL+"/api/cookie", `{"cookie":"sid=page-secret"}`, c)
	r.Body.Close()
	r = sendJSON(t, http.MethodDelete, srv.URL+"/api/cookie", "", c)
	if body := decode(t, r); r.StatusCode != http.StatusOK || body["source"] != "none" {
		t.Fatalf("clear shared: %s %v", r.Status, body)
	}
	rt, _, _ := boards.Resolve(b.ID)
	if rt.Fetcher.Cookie().Source != "none" {
		t.Fatal("shared clear did not remove credentials")
	}
}

func TestBoardAdminSelectionAndCrossOrigin(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "failed", http.StatusBadGateway)
	}))
	defer source.Close()
	cfg := &config.Config{Admin: config.Admin{Username: "admin", Password: "pw", SessionTTL: time.Hour}, Magma: config.Magma{Timeout: time.Second}}
	s, boards, _ := newTestWebServer(t, cfg, false)
	a := createBoard(t, boards, "A", false)
	b := createBoard(t, boards, "B", false)
	settings := a.Settings
	settings.URL, settings.Schedule = source.URL, "0 * * * *"
	if _, err := boards.Update(a.ID, settings); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	c := login(t, srv.URL)
	r := sendJSON(t, http.MethodPost, srv.URL+"/api/schedule?board="+a.ID, `{"paused":false}`, c)
	body := decode(t, r)
	if r.StatusCode != http.StatusOK || body["schedule"] != settings.Schedule || body["board"].(map[string]any)["id"] != a.ID {
		t.Fatalf("schedule: %s %v", r.Status, body)
	}
	rtA, _, _ := boards.Resolve(a.ID)
	rtB, _, _ := boards.Resolve(b.ID)
	if rtA.Fetcher.Status().Paused || !rtB.Fetcher.Status().Paused {
		t.Fatal("schedule changed another board")
	}
	r = sendJSON(t, http.MethodPost, srv.URL+"/api/fetch?board="+a.ID, "", c)
	r.Body.Close()
	if r.StatusCode != http.StatusOK || len(rtA.Fetcher.History()) != 1 || len(rtB.Fetcher.History()) != 0 {
		t.Fatalf("fetch did not stay on selected board: %s", r.Status)
	}
	body = decode(t, get(t, srv.URL+"/api/status?board="+a.ID, c))
	if body["schedule"] != settings.Schedule || body["board"].(map[string]any)["id"] != a.ID {
		t.Fatalf("status: %v", body)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/admin/boards", strings.NewReader(`{"name":"Cross","url":"http://example.invalid"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.AddCookie(c)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if body := decode(t, r); r.StatusCode != http.StatusForbidden || body["code"] != "cross_origin" {
		t.Fatalf("cross-origin board create: %s %v", r.Status, body)
	}
}

func TestReadyChecksEveryBoard(t *testing.T) {
	s, boards, _ := newTestWebServer(t, &config.Config{}, true)
	b := createBoard(t, boards, "Other", false)
	h := s.Handler()
	check := func(want int) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		var body map[string]any
		_ = json.NewDecoder(rec.Body).Decode(&body)
		if rec.Code != want || body["boards"] != float64(2) || body["storage_writable"] != (want == http.StatusOK) {
			t.Fatalf("ready: %d %v", rec.Code, body)
		}
	}
	check(http.StatusOK)
	rt, _, _ := boards.Resolve(b.ID)
	if err := os.RemoveAll(rt.Store.Dir()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rt.Store.Dir(), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.ready.mu.Lock()
	s.ready.expires = time.Now().Add(-time.Second)
	s.ready.mu.Unlock()
	check(http.StatusServiceUnavailable)
}

func TestAdminHistoryLimit(t *testing.T) {
	cfg := &config.Config{Admin: config.Admin{Username: "admin", Password: "pw", SessionTTL: time.Hour}}
	s, boards, _ := newTestWebServer(t, cfg, false)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		b := createBoard(t, boards, fmt.Sprintf("Board %d", i), false)
		rt, _, _ := boards.Resolve(b.ID)
		runs := make([]fetcher.Run, 50)
		for n := range runs {
			runs[n] = fetcher.Run{Time: start.Add(time.Duration(i*50+n) * time.Minute), Result: fetcher.ResultError}
		}
		data, err := json.Marshal(runs)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(rt.Store.Dir(), ".state", "history.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := board.NewManager(board.Options{
		Root: cfg.Storage.Dir, TempDir: cfg.Storage.TempDir, Magma: cfg.Magma, Schedule: cfg.Schedule, Shared: s.creds,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.Stop)
	s.boards = reopened
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	c := login(t, srv.URL)
	items := decode(t, get(t, srv.URL+"/api/admin/history", c))["items"].([]any)
	if len(items) != 200 {
		t.Fatalf("history length %d, want 200", len(items))
	}
	for i, raw := range items {
		item := raw.(map[string]any)
		when, err := time.Parse(time.RFC3339Nano, item["time"].(string))
		if err != nil || !when.Equal(start.Add(time.Duration(249-i)*time.Minute)) {
			t.Fatalf("history item %d: %v, %v", i, item, err)
		}
	}
}
