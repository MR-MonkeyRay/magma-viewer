package board

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"magma-viewer/internal/store"
)

func TestBuildArchive(t *testing.T) {
	root := t.TempDir()
	boardDir := filepath.Join(root, "board")
	if err := os.MkdirAll(boardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"20261103-010203_0123456789abcdef0123456789abcdef.png": []byte("first image"),
		"20261103-020304_fedcba9876543210fedcba9876543210.jpg": []byte("second image"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(boardDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.New(boardDir)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 11, 3, 10, 11, 12, 0, time.FixedZone("west", -8*60*60))
	b := Board{ID: "b-0123456789abcdef", Settings: Settings{Name: "Board", URL: "https://example.com"}}
	var progress [][2]int
	a, err := buildArchive(context.Background(), filepath.Join(root, "archives"), b, st, created, func(done, total int) {
		progress = append(progress, [2]int{done, total})
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != "" || a.File != "b-0123456789abcdef--20261103-181112.zip" || a.Images != len(files) || a.Size == 0 || !a.Created.Equal(created) || a.Created.Location() != time.UTC {
		t.Fatalf("archive = %+v", a)
	}
	if len(progress) != len(files)+1 || progress[0] != [2]int{0, len(files)} || progress[len(progress)-1] != [2]int{len(files), len(files)} {
		t.Fatalf("progress = %v", progress)
	}

	zr, err := zip.OpenReader(filepath.Join(root, "archives", a.File))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	got := make(map[string][]byte)
	for _, zf := range zr.File {
		r, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(r)
		closeErr := r.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read %s: %v, close: %v", zf.Name, readErr, closeErr)
		}
		got[zf.Name] = data
		if zf.Name != manifestName && zf.Method != zip.Store {
			t.Errorf("%s method = %d", zf.Name, zf.Method)
		}
	}
	for name, want := range files {
		if !bytes.Equal(got[name], want) {
			t.Errorf("%s = %q, want %q", name, got[name], want)
		}
	}
	manifest := string(got[manifestName])
	for _, want := range []string{b.ID, b.Name, `"images":2`, "2026-11-03T18:11:12Z"} {
		if !strings.Contains(manifest, want) {
			t.Errorf("manifest %q does not contain %q", manifest, want)
		}
	}
}

func TestBuildArchiveSourceFailureCleansPart(t *testing.T) {
	root := t.TempDir()
	boardDir := filepath.Join(root, "board")
	if err := os.MkdirAll(boardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "20261103-010203_0123456789abcdef0123456789abcdef.png"
	path := filepath.Join(boardDir, name)
	if err := os.WriteFile(path, []byte("source stays until snapshot"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(boardDir)
	if err != nil {
		t.Fatal(err)
	}
	archives := filepath.Join(root, "archives")
	_, err = buildArchive(context.Background(), archives, Board{ID: LegacyID, Settings: Settings{Name: "Main"}}, st, time.Now(), func(done, _ int) {
		if done == 0 {
			if removeErr := os.Remove(path); removeErr != nil {
				t.Fatal(removeErr)
			}
		}
	})
	if err == nil {
		t.Fatal("buildArchive succeeded after the snapshotted source disappeared")
	}
	entries, readErr := os.ReadDir(archives)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("failed build left files: %v", entries)
	}
}

func TestBuildArchiveCancellationCleansPart(t *testing.T) {
	root := t.TempDir()
	boardDir := filepath.Join(root, "board")
	if err := os.MkdirAll(boardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(boardDir, "one.png"), []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(boardDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	archives := filepath.Join(root, "archives")
	_, err = buildArchive(ctx, archives, Board{ID: LegacyID, Settings: Settings{Name: "Main"}}, st, time.Now(), func(done, _ int) {
		if done == 0 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("buildArchive error = %v", err)
	}
	entries, readErr := os.ReadDir(archives)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("cancelled build left files: %v", entries)
	}
}

func TestBuildArchiveRefusesExistingFinal(t *testing.T) {
	root := t.TempDir()
	boardDir := filepath.Join(root, "board")
	if err := os.MkdirAll(boardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(boardDir)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 11, 3, 1, 2, 3, 0, time.UTC)
	archives := filepath.Join(root, "archives")
	if err := os.MkdirAll(archives, 0o755); err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(archives, LegacyID+"--20261103-010203.zip")
	if err := os.WriteFile(final, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = buildArchive(context.Background(), archives, Board{ID: LegacyID, Settings: Settings{Name: "Main"}}, st, created, nil)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("buildArchive error = %v", err)
	}
	data, readErr := os.ReadFile(final)
	if readErr != nil || string(data) != "keep" {
		t.Fatalf("existing final = %q, %v", data, readErr)
	}
}

func TestVerifyArchiveRejectsDamageAndWrongSet(t *testing.T) {
	makeArchive := func(t *testing.T) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "test.zip")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		zw := zip.NewWriter(f)
		for name, data := range map[string]string{"image.png": "image bytes", manifestName: `{}`} {
			h := &zip.FileHeader{Name: name, Method: zip.Store}
			w, err := zw.CreateHeader(h)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(w, data); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("wrong expected size", func(t *testing.T) {
		if err := verifyArchive(makeArchive(t), map[string]int64{"image.png": 1}); err == nil {
			t.Fatal("verifyArchive accepted a wrong size")
		}
	})
	t.Run("missing expected entry", func(t *testing.T) {
		if err := verifyArchive(makeArchive(t), map[string]int64{"image.png": 11, "other.png": 1}); err == nil {
			t.Fatal("verifyArchive accepted a missing entry")
		}
	})
	t.Run("unexpected entry", func(t *testing.T) {
		if err := verifyArchive(makeArchive(t), map[string]int64{}); err == nil {
			t.Fatal("verifyArchive accepted an unexpected entry")
		}
	})
	t.Run("CRC", func(t *testing.T) {
		path := makeArchive(t)
		zr, err := zip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		var offset int64
		for _, zf := range zr.File {
			if zf.Name == "image.png" {
				offset, err = zf.DataOffset()
			}
		}
		zr.Close()
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteAt([]byte{'X'}, offset); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if err := verifyArchive(path, map[string]int64{"image.png": 11}); err == nil {
			t.Fatal("verifyArchive accepted a CRC mismatch")
		}
	})
	t.Run("truncated", func(t *testing.T) {
		path := makeArchive(t)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, info.Size()-8); err != nil {
			t.Fatal(err)
		}
		if err := verifyArchive(path, map[string]int64{"image.png": 11}); err == nil {
			t.Fatal("verifyArchive accepted a truncated zip")
		}
	})
}
