package web

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"magma-viewer/internal/board"
	"magma-viewer/internal/config"
)

func TestAdminMaintenanceRoutesRequireLoginAndOrigin(t *testing.T) {
	s, _, _ := newTestWebServer(t, &config.Config{Admin: config.Admin{Username: "admin", Password: "pw", SessionTTL: time.Hour}}, false)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/admin/boards/missing/archive"},
		{http.MethodDelete, "/api/admin/boards/missing"},
		{http.MethodGet, "/api/admin/boards/missing/operation"},
		{http.MethodGet, "/api/admin/archives"},
		{http.MethodGet, "/api/admin/archives/missing/download"},
		{http.MethodDelete, "/api/admin/archives/missing"},
	}
	for _, route := range routes {
		req, err := http.NewRequest(route.method, srv.URL+route.path, strings.NewReader(`{"confirm":"x"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("guest %s %s: %s", route.method, route.path, resp.Status)
		}
	}

	cookie := login(t, srv.URL)
	for _, route := range routes {
		if route.method == http.MethodGet {
			continue
		}
		req, err := http.NewRequest(route.method, srv.URL+route.path, strings.NewReader(`{"confirm":"x"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(cookie)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("cross-site %s %s: %s", route.method, route.path, resp.Status)
		}
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/admin/archives/missing/download", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site archive download: %s", resp.Status)
	}

	operation := get(t, srv.URL+"/api/admin/boards/missing/operation", cookie)
	operationBody := decode(t, operation)
	if operation.StatusCode != http.StatusNotFound || operationBody["code"] != "operation_not_found" {
		t.Fatalf("unknown operation: %s %v", operation.Status, operationBody)
	}
	download := get(t, srv.URL+"/api/admin/archives/missing/download", cookie)
	downloadBody := decode(t, download)
	if download.StatusCode != http.StatusNotFound || downloadBody["code"] != "archive_not_found" {
		t.Fatalf("unknown archive download: %s %v", download.Status, downloadBody)
	}
}

func TestAdminArchiveLifecycle(t *testing.T) {
	s, boards, _ := newTestWebServer(t, &config.Config{Admin: config.Admin{Username: "admin", Password: "pw", SessionTTL: time.Hour}}, false)
	archived := createBoard(t, boards, "Archive Me", false)
	other := createBoard(t, boards, "Keep Me", false)
	archivedNames := importArchiveImages(t, boards, archived.ID, "archive")
	otherNames := importArchiveImages(t, boards, other.ID, "other")
	boards.Start(context.Background())

	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	cookie := login(t, srv.URL)

	resp := sendJSON(t, http.MethodPost, srv.URL+"/api/admin/boards/"+archived.ID+"/archive", `{}`, cookie)
	body := decode(t, resp)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("archive: %s %v", resp.Status, body)
	}
	if _, ok := body["operation"].(map[string]any); !ok {
		t.Fatalf("archive response has no operation: %v", body)
	}

	op := waitOperation(t, srv.URL, archived.ID, cookie)
	if op["kind"] != "archive" || op["phase"] != "done" || op["error"] != nil {
		t.Fatalf("archive operation: %v", op)
	}

	archivesResponse := get(t, srv.URL+"/api/admin/archives", cookie)
	archivesBody := decode(t, archivesResponse)
	if archivesResponse.StatusCode != http.StatusOK {
		t.Fatalf("archives: %s %v", archivesResponse.Status, archivesBody)
	}
	items := archivesBody["items"].([]any)
	if len(items) != 1 || archivesBody["bytes"].(float64) <= 0 {
		t.Fatalf("archives: %v", archivesBody)
	}
	archive := items[0].(map[string]any)
	archiveID := archive["id"].(string)
	archivePath, _, err := boards.ArchiveFile(archiveID)
	if err != nil {
		t.Fatal(err)
	}

	download := get(t, srv.URL+"/api/admin/archives/"+archiveID+"/download", cookie)
	data, err := io.ReadAll(download.Body)
	download.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if download.StatusCode != http.StatusOK || !strings.HasPrefix(download.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("download: %s disposition=%q", download.Status, download.Header.Get("Content-Disposition"))
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	zipNames := make(map[string]bool, len(zr.File))
	for _, file := range zr.File {
		zipNames[file.Name] = true
		reader, openErr := file.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		contents, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read archive entry %s: %v, close: %v", file.Name, readErr, closeErr)
		}
		if file.Name == archivedNames[0] && string(contents) != "archive-0" {
			t.Errorf("archive entry %s = %q", file.Name, contents)
		}
	}
	for _, name := range archivedNames {
		if !zipNames[name] {
			t.Errorf("archive is missing %s", name)
		}
	}
	if download.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("archive download Cache-Control = %q", download.Header.Get("Cache-Control"))
	}

	for _, path := range []string{"/api/images?board=" + archived.ID, "/img/" + archivedNames[0] + "?board=" + archived.ID} {
		resp := get(t, srv.URL+path, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("archived board %s: %s", path, resp.Status)
		}
	}
	publicBoards := decode(t, get(t, srv.URL+"/api/boards", nil))
	for _, raw := range publicBoards["items"].([]any) {
		if raw.(map[string]any)["id"] == archived.ID {
			t.Fatalf("archived board remains public: %v", publicBoards)
		}
	}
	adminBoards := decode(t, get(t, srv.URL+"/api/admin/boards", cookie))
	foundArchived := false
	for _, raw := range adminBoards["others"].([]any) {
		item := raw.(map[string]any)
		if item["id"] == archived.ID && item["lifecycle"] == "archived" {
			foundArchived = true
		}
	}
	if !foundArchived {
		t.Fatalf("archived board missing from others: %v", adminBoards)
	}

	overview := decode(t, get(t, srv.URL+"/api/admin/overview", cookie))
	archiveOverview := overview["archives"].(map[string]any)
	if archiveOverview["count"] != float64(1) || archiveOverview["bytes"].(float64) <= 0 || overview["maintenance"] != nil {
		t.Fatalf("overview archive fields: %v", overview)
	}

	otherImage := get(t, srv.URL+"/img/"+otherNames[0]+"?board="+other.ID, nil)
	otherData, err := io.ReadAll(otherImage.Body)
	otherImage.Body.Close()
	if err != nil || otherImage.StatusCode != http.StatusOK || string(otherData) != "other-0" {
		t.Fatalf("other board image: %s %q, %v", otherImage.Status, otherData, err)
	}

	deleteResponse := sendJSON(t, http.MethodDelete, srv.URL+"/api/admin/archives/"+archiveID, `{}`, cookie)
	deleteBody := decode(t, deleteResponse)
	if deleteResponse.StatusCode != http.StatusOK || len(deleteBody["items"].([]any)) != 0 {
		t.Fatalf("delete archive: %s %v", deleteResponse.Status, deleteBody)
	}
	if _, err := os.Stat(archivePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("archive file still exists: %v", err)
	}
	missing := sendJSON(t, http.MethodDelete, srv.URL+"/api/admin/archives/"+archiveID, `{}`, cookie)
	missingBody := decode(t, missing)
	if missing.StatusCode != http.StatusNotFound || missingBody["code"] != "archive_not_found" {
		t.Fatalf("delete unknown archive: %s %v", missing.Status, missingBody)
	}
}

func TestAdminDeleteBoard(t *testing.T) {
	s, boards, _ := newTestWebServer(t, &config.Config{Admin: config.Admin{Username: "admin", Password: "pw", SessionTTL: time.Hour}}, false)
	deleted := createBoard(t, boards, "Delete Me", false)
	other := createBoard(t, boards, "Still Here", false)
	deletedNames := importArchiveImages(t, boards, deleted.ID, "delete")
	otherNames := importArchiveImages(t, boards, other.ID, "still")
	boards.Start(context.Background())

	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	cookie := login(t, srv.URL)

	wrong := sendJSON(t, http.MethodDelete, srv.URL+"/api/admin/boards/"+deleted.ID, `{"confirm":"wrong"}`, cookie)
	wrongBody := decode(t, wrong)
	if wrong.StatusCode != http.StatusBadRequest || wrongBody["code"] != "confirm_mismatch" {
		t.Fatalf("wrong confirmation: %s %v", wrong.Status, wrongBody)
	}
	if _, ok := boards.Board(deleted.ID); !ok {
		t.Fatal("wrong confirmation deleted the board")
	}
	image := get(t, srv.URL+"/img/"+deletedNames[0]+"?board="+deleted.ID, nil)
	image.Body.Close()
	if image.StatusCode != http.StatusOK {
		t.Fatalf("wrong confirmation removed data: %s", image.Status)
	}
	for _, body := range []string{`{`, `{"confirm":7}`, `{"confirm":"` + strings.Repeat("x", 5<<10) + `"}`} {
		invalid := sendJSON(t, http.MethodDelete, srv.URL+"/api/admin/boards/"+deleted.ID, body, cookie)
		invalidBody := decode(t, invalid)
		if invalid.StatusCode != http.StatusBadRequest || invalidBody["code"] != "bad_request" {
			t.Errorf("invalid delete body: %s %v", invalid.Status, invalidBody)
		}
	}

	deletedDir := board.BoardDir(s.cfg.Storage.Dir, deleted.ID)
	accepted := sendJSON(t, http.MethodDelete, srv.URL+"/api/admin/boards/"+deleted.ID, `{"confirm":"Delete Me"}`, cookie)
	acceptedBody := decode(t, accepted)
	if accepted.StatusCode != http.StatusAccepted {
		t.Fatalf("delete: %s %v", accepted.Status, acceptedBody)
	}
	op := waitOperation(t, srv.URL, deleted.ID, cookie)
	if op["kind"] != "delete" || op["phase"] != "done" || op["error"] != nil {
		t.Fatalf("delete operation: %v", op)
	}
	if _, ok := boards.Board(deleted.ID); ok {
		t.Fatal("deleted board remains registered")
	}
	if _, err := os.Stat(deletedDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted board directory remains: %v", err)
	}
	for _, archive := range boards.Archives() {
		if archive.BoardID == deleted.ID {
			t.Fatalf("deleted board archive remains: %+v", archive)
		}
	}
	adminBoards := decode(t, get(t, srv.URL+"/api/admin/boards", cookie))
	for _, key := range []string{"items", "others"} {
		for _, raw := range adminBoards[key].([]any) {
			if raw.(map[string]any)["id"] == deleted.ID {
				t.Fatalf("deleted board remains in %s: %v", key, adminBoards)
			}
		}
	}
	otherImage := get(t, srv.URL+"/img/"+otherNames[0]+"?board="+other.ID, nil)
	otherData, err := io.ReadAll(otherImage.Body)
	otherImage.Body.Close()
	if err != nil || otherImage.StatusCode != http.StatusOK || string(otherData) != "still-0" {
		t.Fatalf("other board image: %s %q, %v", otherImage.Status, otherData, err)
	}
}

func TestDeleteArchivedBoardRemovesArchive(t *testing.T) {
	s, boards, _ := newTestWebServer(t, &config.Config{Admin: config.Admin{Username: "admin", Password: "pw", SessionTTL: time.Hour}}, false)
	target := createBoard(t, boards, "Archived Delete", false)
	importArchiveImages(t, boards, target.ID, "archived-delete")
	boards.Start(context.Background())
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	cookie := login(t, srv.URL)

	archiveResponse := sendJSON(t, http.MethodPost, srv.URL+"/api/admin/boards/"+target.ID+"/archive", `{}`, cookie)
	archiveResponse.Body.Close()
	if archiveResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("archive: %s", archiveResponse.Status)
	}
	waitOperation(t, srv.URL, target.ID, cookie)
	archives := boards.Archives()
	if len(archives) != 1 {
		t.Fatalf("archives = %+v", archives)
	}
	archivePath, _, err := boards.ArchiveFile(archives[0].ID)
	if err != nil {
		t.Fatal(err)
	}

	deleteResponse := sendJSON(t, http.MethodDelete, srv.URL+"/api/admin/boards/"+target.ID, `{"confirm":"Archived Delete"}`, cookie)
	deleteBody := decode(t, deleteResponse)
	if deleteResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("delete archived board: %s %v", deleteResponse.Status, deleteBody)
	}
	waitOperation(t, srv.URL, target.ID, cookie)
	if len(boards.Archives()) != 0 {
		t.Fatalf("delete left registered archives: %+v", boards.Archives())
	}
	if _, err := os.Stat(archivePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("delete left archive file: %v", err)
	}
}

func TestFailedArchiveAppearsOnActiveBoard(t *testing.T) {
	s, boards, _ := newTestWebServer(t, &config.Config{Admin: config.Admin{Username: "admin", Password: "pw", SessionTTL: time.Hour}}, false)
	target := createBoard(t, boards, "Failure", false)
	importArchiveImages(t, boards, target.ID, "failure")
	if err := os.WriteFile(filepath.Join(s.cfg.Storage.Dir, "archives"), []byte("block directory creation"), 0o644); err != nil {
		t.Fatal(err)
	}
	boards.Start(context.Background())
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	cookie := login(t, srv.URL)

	archiveResponse := sendJSON(t, http.MethodPost, srv.URL+"/api/admin/boards/"+target.ID+"/archive", `{}`, cookie)
	archiveResponse.Body.Close()
	if archiveResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("archive: %s", archiveResponse.Status)
	}
	op := waitOperation(t, srv.URL, target.ID, cookie)
	if op["phase"] != "failed" || op["error"] == "" {
		t.Fatalf("failed operation: %v", op)
	}
	b, ok := boards.Board(target.ID)
	if !ok || b.Lifecycle != board.Active {
		t.Fatalf("failed archive lifecycle: %+v, exists=%v", b, ok)
	}
	adminBoards := decode(t, get(t, srv.URL+"/api/admin/boards", cookie))
	found := false
	for _, raw := range adminBoards["items"].([]any) {
		item := raw.(map[string]any)
		if item["id"] == target.ID {
			operation, exists := item["operation"].(map[string]any)
			found = exists && operation["phase"] == "failed"
		}
	}
	if !found {
		t.Fatalf("active board missing failed operation: %v", adminBoards)
	}
}

func TestRunningMaintenanceOverviewAndConflicts(t *testing.T) {
	s, boards, _ := newTestWebServer(t, &config.Config{Admin: config.Admin{Username: "admin", Password: "pw", SessionTTL: time.Hour}}, false)
	b := createBoard(t, boards, "Held", false)
	other := createBoard(t, boards, "Other", false)
	rt, _, err := boards.Resolve(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	releaseRun := sync.OnceFunc(func() { close(release) })
	defer releaseRun()
	// Hold the fetch lock so maintenance waits in Drain without doing network I/O.
	rt.Fetcher.Active = func() bool {
		close(entered)
		<-release
		return false
	}
	runDone := make(chan error, 1)
	go func() {
		_, runErr := rt.Fetcher.Run(t.Context())
		runDone <- runErr
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("fetch did not acquire its lock")
	}
	token, _ := s.auth.newToken()
	h := s.Handler()
	request := func(method, target, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := request(http.MethodPost, "/api/admin/boards/"+b.ID+"/archive", "{}"); rec.Code != http.StatusAccepted {
		t.Fatalf("archive: %d %s", rec.Code, rec.Body)
	}
	rec := request(http.MethodGet, "/api/admin/overview", "")
	var overview struct {
		Maintenance *board.Operation `json:"maintenance"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&overview); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || overview.Maintenance == nil || overview.Maintenance.BoardID != b.ID || !overview.Maintenance.Finished.IsZero() {
		t.Fatalf("running maintenance overview: %d %+v", rec.Code, overview.Maintenance)
	}
	for _, tc := range []struct{ method, target, body string }{
		{http.MethodPost, "/api/admin/boards/" + b.ID + "/archive", "{}"},
		{http.MethodDelete, "/api/admin/boards/" + other.ID, `{"confirm":"Other"}`},
	} {
		rec := request(tc.method, tc.target, tc.body)
		var body map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusConflict || body["code"] != "maintenance_busy" {
			t.Fatalf("running maintenance conflict: %d %v", rec.Code, body)
		}
	}
	releaseRun()
	if err := <-runDone; err == nil {
		t.Fatal("inactive fetch unexpectedly succeeded")
	}
}

func TestMaintenanceErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	writeBoardError(rec, board.ErrMaintenanceBusy)
	if rec.Code != http.StatusConflict {
		t.Fatalf("maintenance busy status: %d", rec.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != "maintenance_busy" {
		t.Fatalf("maintenance busy body: %v", body)
	}
}

func importArchiveImages(t *testing.T, boards *board.Manager, id, prefix string) []string {
	t.Helper()
	rt, _, err := boards.Resolve(id)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{
		"20261103-010203_0123456789abcdef0123456789abcdef.png",
		"20261103-020304_fedcba9876543210fedcba9876543210.jpg",
	}
	for i, name := range names {
		if err := rt.Store.Import(strings.NewReader(prefix+"-"+string(rune('0'+i))), name); err != nil {
			t.Fatal(err)
		}
	}
	return names
}

func waitOperation(t *testing.T, base, id string, cookie *http.Cookie) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp := get(t, base+"/api/admin/boards/"+id+"/operation", cookie)
		body := decode(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("operation: %s %v", resp.Status, body)
		}
		if _, ok := body["finished"].(string); ok {
			return body
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("operation %s did not finish", id)
	return nil
}
