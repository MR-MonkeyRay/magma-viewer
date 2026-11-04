package board

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"magma-viewer/internal/fsutil"
	"magma-viewer/internal/store"
)

// RegistryPath returns the shared board registry path.
func RegistryPath(root string) string { return filepath.Join(root, ".state", "boards.json") }

// BoardDir returns a board's storage directory.
func BoardDir(root, id string) string { return filepath.Join(root, "boards", id) }

// Open loads <root>/.state/boards.json, migrating a single-board layout into
// boards/default first when the registry does not exist yet.
func Open(root string, now time.Time) (*Registry, error) {
	path := RegistryPath(root)
	if _, err := os.Lstat(path); err == nil {
		return Load(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	dest := BoardDir(root, LegacyID)
	for _, dir := range []string{filepath.Dir(dest), dest} {
		info, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("migration directory is a symbolic link: %s", dir)
		}
	}

	// Include destination entries so a crash after the last rename can resume.
	names := make(map[string]bool)
	for _, dir := range []string{root, dest} {
		entries, err := legacyEntries(dir)
		if err != nil {
			return nil, err
		}
		for _, name := range entries {
			names[name] = true
		}
	}
	st := State{Version: Version}
	if len(names) == 0 {
		return Create(path, st)
	}

	var candidates []string
	for name := range names {
		candidates = append(candidates, name)
	}
	slices.Sort(candidates)
	var pending []string
	for _, name := range candidates {
		src, dst := filepath.Join(root, name), filepath.Join(dest, name)
		_, srcErr := os.Lstat(src)
		if srcErr != nil && !errors.Is(srcErr, os.ErrNotExist) {
			return nil, srcErr
		}
		_, dstErr := os.Lstat(dst)
		if dstErr != nil && !errors.Is(dstErr, os.ErrNotExist) {
			return nil, dstErr
		}
		if srcErr == nil && dstErr == nil {
			return nil, fmt.Errorf("migration conflict: %s and %s both exist", src, dst)
		}
		if srcErr == nil {
			pending = append(pending, name)
		}
	}

	if err := os.MkdirAll(filepath.Join(dest, ".state"), 0o755); err != nil {
		return nil, err
	}
	for _, name := range pending {
		if err := os.Rename(filepath.Join(root, name), filepath.Join(dest, name)); err != nil {
			return nil, err
		}
	}
	// Directory sync is best effort: unsupported sync weakens durability but
	// does not undo completed renames. The registry is the final commit marker.
	for _, dir := range []string{dest, filepath.Join(dest, ".state"), filepath.Dir(dest), root, filepath.Join(root, ".state")} {
		_ = fsutil.SyncDir(dir)
	}
	st.LegacyBoardID = LegacyID
	st.Boards = []Board{{
		ID:        LegacyID,
		Settings:  Settings{Name: "默认画板"},
		Lifecycle: Active,
		Created:   now.UTC(),
	}}
	return Create(path, st)
}

func legacyEntries(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if !store.IsAllowedExt(filepath.Ext(name)) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("migration image is a symbolic link: %s; resolve it manually before retrying", filepath.Join(dir, name))
		}
		if !strings.HasPrefix(name, ".") && info.Mode().IsRegular() {
			names = append(names, name)
		}
	}
	for _, name := range []string{
		".thumbs",
		filepath.Join(".state", "fetcher.json"),
		filepath.Join(".state", "history.json"),
		filepath.Join(".state", "archive.json"),
	} {
		info, err := os.Lstat(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if (name == ".thumbs" && info.IsDir()) || (name != ".thumbs" && info.Mode().IsRegular()) {
			names = append(names, name)
		}
	}
	return names, nil
}
