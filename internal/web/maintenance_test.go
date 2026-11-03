package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"magma-viewer/internal/config"
)

func TestZipAbortsAfterBoardArchived(t *testing.T) {
	s, boards, _ := newTestWebServer(t, &config.Config{
		Admin: config.Admin{Username: "admin", Password: "pw", SessionTTL: time.Hour},
	}, false)
	b := createBoard(t, boards, "Zip", false)
	importArchiveImages(t, boards, b.ID, "zip")
	rt, _, err := boards.Resolve(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	items := rt.Store.All()
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	cookie := login(t, srv.URL)
	if err := boards.Archive(b.ID); err != nil {
		t.Fatal(err)
	}
	if op := waitOperation(t, srv.URL, b.ID, cookie); op["phase"] != "done" {
		t.Fatalf("archive: %v", op)
	}
	if _, _, err := boards.Resolve(b.ID); err == nil {
		t.Fatal("archived board still resolves")
	}
	if _, err := os.Stat(filepath.Join(rt.Store.Dir(), items[0].Name)); !os.IsNotExist(err) {
		t.Fatalf("source file should be removed: %v", err)
	}
	rec := httptest.NewRecorder()
	defer func() {
		if got := recover(); got != http.ErrAbortHandler {
			t.Fatalf("writeZip panic = %v, want http.ErrAbortHandler", got)
		}
		if _, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len())); err == nil {
			t.Fatal("aborted ZIP was finalized")
		}
	}()
	s.writeZip(rec, httptest.NewRequest(http.MethodGet, "/download/all", nil), b.ID, rt.Store, "test.zip", items)
}

type maintenanceReader struct {
	io.Reader
	before func()
}

func (r *maintenanceReader) Read(p []byte) (int, error) {
	r.before()
	return r.Reader.Read(p)
}

func TestScheduleBoardRetiresDuringRequest(t *testing.T) {
	s, boards, _ := newTestWebServer(t, &config.Config{}, false)
	b := createBoard(t, boards, "Schedule", false)
	body := &maintenanceReader{
		Reader: strings.NewReader(`{"paused":false}`),
		before: sync.OnceFunc(func() {
			if err := boards.Archive(b.ID); err != nil {
				t.Fatal(err)
			}
		}),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/schedule?board="+b.ID, body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleSchedule(rec, req)
	var response map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusNotFound || response["code"] != "board_not_found" {
		t.Fatalf("retired schedule: %d %v", rec.Code, response)
	}
}

func TestArchiveDownloadOpenFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires Unix sockets")
	}
	s, boards, _ := newTestWebServer(t, &config.Config{}, false)
	b := createBoard(t, boards, "Download", false)
	if err := boards.Archive(b.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		op, _ := boards.Operation(b.ID)
		if !op.Finished.IsZero() {
			if op.Err != "" {
				t.Fatal(op.Err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("archive did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	boards.Stop()
	archives := boards.Archives()
	if len(archives) != 1 {
		t.Fatalf("archives = %+v", archives)
	}
	path, _, err := boards.ArchiveFile(archives[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	// A Unix socket passes ArchiveFile's Stat but cannot be opened as a file.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if _, _, err := boards.ArchiveFile(archives[0].ID); err != nil {
		t.Fatalf("test requires archive lookup to succeed: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/archives/"+archives[0].ID+"/download", nil)
	req.SetPathValue("id", archives[0].ID)
	rec := httptest.NewRecorder()
	s.handleDownloadArchive(rec, req)
	var response map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusNotFound || response["code"] != "archive_not_found" {
		t.Fatalf("unreadable archive: %d %v", rec.Code, response)
	}
	if disposition := rec.Header().Get("Content-Disposition"); disposition != "" {
		t.Fatalf("failed archive download has attachment header: %q", disposition)
	}
}
