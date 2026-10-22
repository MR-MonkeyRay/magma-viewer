// Package fsutil writes files so that a crash or power loss leaves either the
// old or the new content, never a truncated file under the final name.
package fsutil

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// WriteFile atomically replaces path with data.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := WriteTemp(dir, "."+filepath.Base(path), bytes.NewReader(data), perm)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	// The new content is in place now. A failed directory sync only weakens
	// durability; reporting it as a failed write would make callers keep the
	// old state in memory while the file already holds the new one.
	_ = SyncDir(dir)
	return nil
}

// WriteTemp copies r into a new file in dir and syncs it, returning its path.
// perm is filtered by the umask, as with os.WriteFile. The caller renames the
// file into place (on the same file system) or removes it.
func WriteTemp(dir, prefix string, r io.Reader, perm os.FileMode) (string, error) {
	var f *os.File
	var err error
	for range 3 {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		f, err = os.OpenFile(filepath.Join(dir, prefix+"."+hex.EncodeToString(b)+".part"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if !errors.Is(err, os.ErrExist) {
			break
		}
	}
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, r)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// SyncDir makes a rename into dir durable.
func SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
