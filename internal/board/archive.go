package board

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"magma-viewer/internal/fsutil"
	"magma-viewer/internal/store"
)

const manifestName = "manifest.json"

type archiveManifest struct {
	BoardID string    `json:"board_id"`
	Name    string    `json:"name"`
	Images  int       `json:"images"`
	Created time.Time `json:"created"`
}

// buildArchive writes and verifies one complete board archive before making it
// visible under its final name. The returned archive has no ID; the registry
// commit assigns one.
func buildArchive(ctx context.Context, archivesDir string, b Board, st *store.Store, created time.Time, progress func(done, total int)) (_ Archive, retErr error) {
	if err := ctx.Err(); err != nil {
		return Archive{}, err
	}
	if err := st.Rescan(); err != nil {
		return Archive{}, fmt.Errorf("rescan board: %w", err)
	}
	items := st.All()
	if progress != nil {
		progress(0, len(items))
	}
	if err := ctx.Err(); err != nil {
		return Archive{}, err
	}
	if err := os.MkdirAll(archivesDir, 0o755); err != nil {
		return Archive{}, err
	}

	created = created.UTC()
	name := b.ID + "--" + created.Format("20060102-150405") + ".zip"
	finalPath := filepath.Join(archivesDir, name)
	partPath := filepath.Join(archivesDir, "."+name+".part")
	if _, err := os.Lstat(finalPath); err == nil {
		return Archive{}, fmt.Errorf("archive %s already exists: %w", name, os.ErrExist)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Archive{}, err
	}
	f, err := os.OpenFile(partPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return Archive{}, err
	}
	zw := zip.NewWriter(f)
	fileOpen, zipOpen, renamed := true, true, false
	defer func() {
		if retErr == nil {
			return
		}
		if zipOpen {
			retErr = errors.Join(retErr, zw.Close())
		}
		if fileOpen {
			retErr = errors.Join(retErr, f.Close())
		}
		cleanup := partPath
		if renamed {
			cleanup = finalPath
		}
		if err := os.Remove(cleanup); err != nil && !errors.Is(err, os.ErrNotExist) {
			retErr = errors.Join(retErr, fmt.Errorf("remove incomplete archive: %w", err))
		}
	}()

	expected := make(map[string]int64, len(items))
	for i, item := range items {
		if err := ctx.Err(); err != nil {
			return Archive{}, err
		}
		h := &zip.FileHeader{Name: item.Name, Method: zip.Store}
		h.Modified = item.Time
		w, err := zw.CreateHeader(h)
		if err != nil {
			return Archive{}, fmt.Errorf("create archive entry %s: %w", item.Name, err)
		}
		src, err := os.Open(filepath.Join(st.Dir(), item.Name))
		if err != nil {
			return Archive{}, fmt.Errorf("open source image %s: %w", item.Name, err)
		}
		n, copyErr := io.Copy(w, &contextReader{ctx: ctx, r: src})
		closeErr := src.Close()
		if copyErr != nil {
			return Archive{}, fmt.Errorf("read source image %s: %w", item.Name, copyErr)
		}
		if closeErr != nil {
			return Archive{}, fmt.Errorf("close source image %s: %w", item.Name, closeErr)
		}
		expected[item.Name] = n
		if progress != nil {
			progress(i+1, len(items))
		}
	}

	manifest, err := json.Marshal(archiveManifest{BoardID: b.ID, Name: b.Name, Images: len(items), Created: created})
	if err != nil {
		return Archive{}, err
	}
	w, err := zw.CreateHeader(&zip.FileHeader{Name: manifestName, Method: zip.Store})
	if err != nil {
		return Archive{}, fmt.Errorf("create manifest: %w", err)
	}
	if _, err := io.Copy(w, &contextReader{ctx: ctx, r: bytes.NewReader(manifest)}); err != nil {
		return Archive{}, fmt.Errorf("write manifest: %w", err)
	}
	if err := zw.Close(); err != nil {
		return Archive{}, fmt.Errorf("close zip: %w", err)
	}
	zipOpen = false
	if err := f.Sync(); err != nil {
		return Archive{}, fmt.Errorf("sync archive: %w", err)
	}
	if err := f.Close(); err != nil {
		return Archive{}, fmt.Errorf("close archive: %w", err)
	}
	fileOpen = false
	if err := verifyArchiveContext(ctx, partPath, expected); err != nil {
		return Archive{}, fmt.Errorf("verify archive: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Archive{}, err
	}
	info, err := os.Stat(partPath)
	if err != nil {
		return Archive{}, err
	}
	if err := os.Rename(partPath, finalPath); err != nil {
		return Archive{}, err
	}
	renamed = true
	if err := fsutil.SyncDir(archivesDir); err != nil {
		return Archive{}, fmt.Errorf("sync archives directory: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Archive{}, err
	}
	return Archive{
		BoardID: b.ID, BoardName: b.Name, File: name,
		Images: len(items), Size: info.Size(), Created: created,
	}, nil
}

// verifyArchive fully reads every entry so archive/zip validates its CRC.
func verifyArchive(path string, expected map[string]int64) error {
	return verifyArchiveContext(context.Background(), path, expected)
}

func verifyArchiveContext(ctx context.Context, path string, expected map[string]int64) (retErr error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, zr.Close()) }()

	seen := make(map[string]bool, len(zr.File))
	for _, zf := range zr.File {
		if seen[zf.Name] {
			return fmt.Errorf("duplicate archive entry %q", zf.Name)
		}
		seen[zf.Name] = true
		want, image := expected[zf.Name]
		if !image && zf.Name != manifestName {
			return fmt.Errorf("unexpected archive entry %q", zf.Name)
		}
		if image && zf.UncompressedSize64 != uint64(want) {
			return fmt.Errorf("archive entry %q declares size %d, want %d", zf.Name, zf.UncompressedSize64, want)
		}
		r, err := zf.Open()
		if err != nil {
			return fmt.Errorf("open archive entry %q: %w", zf.Name, err)
		}
		n, copyErr := io.Copy(io.Discard, &contextReader{ctx: ctx, r: r})
		closeErr := r.Close()
		if copyErr != nil {
			return fmt.Errorf("read archive entry %q: %w", zf.Name, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close archive entry %q: %w", zf.Name, closeErr)
		}
		if image && n != want {
			return fmt.Errorf("archive entry %q contains %d bytes, want %d", zf.Name, n, want)
		}
	}
	if !seen[manifestName] {
		return errors.New("archive manifest is missing")
	}
	for name := range expected {
		if !seen[name] {
			return fmt.Errorf("archive entry %q is missing", name)
		}
	}
	if len(seen) != len(expected)+1 {
		return fmt.Errorf("archive has %d entries, want %d", len(seen), len(expected)+1)
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
