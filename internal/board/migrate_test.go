package board

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMigrationPaths(t *testing.T) {
	root := t.TempDir()
	if got, want := RegistryPath(root), filepath.Join(root, ".state", "boards.json"); got != want {
		t.Fatalf("RegistryPath = %q, want %q", got, want)
	}
	if got, want := BoardDir(root, LegacyID), filepath.Join(root, "boards", "default"); got != want {
		t.Fatalf("BoardDir = %q, want %q", got, want)
	}
}

func TestOpenMigratesLegacyLayout(t *testing.T) {
	root := t.TempDir()
	moved := []string{
		"one.jpg", "two.JPEG", "three.png", "four.webp", "five.gif",
		filepath.Join(".thumbs", "one.jpg"),
		filepath.Join(".state", "fetcher.json"),
		filepath.Join(".state", "history.json"),
		filepath.Join(".state", "archive.json"),
	}
	kept := []string{
		"notes.txt", ".hidden.jpg",
		filepath.Join("other", "nested.jpg"),
		filepath.Join("directory.jpg", "nested.jpg"),
		filepath.Join(".state", "cookie.json"),
		filepath.Join(".state", "notices.json"),
		filepath.Join(".state", "tmp", "unfinished.jpg"),
		filepath.Join(".state", "unknown.json"),
	}
	for _, names := range [][]string{moved, kept} {
		for _, name := range names {
			writeMigrationFile(t, filepath.Join(root, name), name)
		}
	}
	mtimes := make(map[string]time.Time)
	for _, name := range append(append([]string{}, moved...), ".thumbs") {
		path := filepath.Join(root, name)
		stamp := time.Unix(1234567890, 123000000)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		mtimes[name] = info.ModTime()
	}
	now := time.Date(2026, 11, 2, 12, 34, 56, 123, time.FixedZone("UTC+8", 8*60*60))
	r, err := Open(root, now)
	if err != nil {
		t.Fatal(err)
	}
	assertMigrationRegistry(t, r, now)
	for _, name := range moved {
		assertMigrationFile(t, filepath.Join(BoardDir(root, LegacyID), name), name)
		assertMigrationAbsent(t, filepath.Join(root, name))
	}
	assertMigrationAbsent(t, filepath.Join(root, ".thumbs"))
	for name, want := range mtimes {
		info, err := os.Stat(filepath.Join(BoardDir(root, LegacyID), name))
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(want) {
			t.Errorf("%s mtime = %v, want %v", name, info.ModTime(), want)
		}
	}
	for _, name := range kept {
		assertMigrationFile(t, filepath.Join(root, name), name)
		assertMigrationAbsent(t, filepath.Join(BoardDir(root, LegacyID), name))
	}
	loaded, err := Load(RegistryPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Snapshot(), r.Snapshot()) {
		t.Fatal("persisted registry differs from Open result")
	}
}

func TestOpenResumesMigration(t *testing.T) {
	for _, allMoved := range []bool{false, true} {
		name := "partially moved"
		if allMoved {
			name = "all moved before registry commit"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			files := []string{
				"one.jpg", "two.jpg", "three.jpg", "four.jpg",
				filepath.Join(".thumbs", "cached.jpg"),
				filepath.Join(".state", "fetcher.json"),
				filepath.Join(".state", "history.json"),
				filepath.Join(".state", "archive.json"),
			}
			for _, file := range files {
				writeMigrationFile(t, filepath.Join(root, file), file)
			}
			if err := os.MkdirAll(filepath.Join(BoardDir(root, LegacyID), ".state"), 0o755); err != nil {
				t.Fatal(err)
			}
			moved := []string{"one.jpg", "two.jpg", filepath.Join(".state", "fetcher.json")}
			if allMoved {
				moved = []string{"one.jpg", "two.jpg", "three.jpg", "four.jpg", ".thumbs",
					filepath.Join(".state", "fetcher.json"), filepath.Join(".state", "history.json"), filepath.Join(".state", "archive.json")}
			}
			for _, file := range moved {
				if err := os.Rename(filepath.Join(root, file), filepath.Join(BoardDir(root, LegacyID), file)); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Unix(1234, 0)
			r, err := Open(root, now)
			if err != nil {
				t.Fatal(err)
			}
			assertMigrationRegistry(t, r, now)
			for _, file := range files {
				assertMigrationFile(t, filepath.Join(BoardDir(root, LegacyID), file), file)
				assertMigrationAbsent(t, filepath.Join(root, file))
			}
		})
	}
}

func TestOpenConflictMovesNothing(t *testing.T) {
	for _, conflict := range []string{"z.jpg", ".thumbs", filepath.Join(".state", "history.json")} {
		t.Run(conflict, func(t *testing.T) {
			root := t.TempDir()
			files := []string{"a.jpg", "z.jpg", filepath.Join(".thumbs", "cached.jpg"),
				filepath.Join(".state", "fetcher.json"), filepath.Join(".state", "history.json"), filepath.Join(".state", "archive.json")}
			for _, name := range files {
				writeMigrationFile(t, filepath.Join(root, name), name)
			}
			dst := filepath.Join(BoardDir(root, LegacyID), conflict)
			if conflict == ".thumbs" {
				if err := os.MkdirAll(dst, 0o755); err != nil {
					t.Fatal(err)
				}
			} else {
				writeMigrationFile(t, dst, "existing destination")
			}
			if _, err := Open(root, time.Now()); err == nil || !strings.Contains(err.Error(), dst) {
				t.Fatalf("conflict error = %v, want path %s", err, dst)
			}
			for _, name := range files {
				assertMigrationFile(t, filepath.Join(root, name), name)
				if name != conflict {
					assertMigrationAbsent(t, filepath.Join(BoardDir(root, LegacyID), name))
				}
			}
			if conflict != ".thumbs" {
				assertMigrationFile(t, dst, "existing destination")
			}
			assertMigrationAbsent(t, RegistryPath(root))
		})
	}
}

func TestOpenImageSymlinkMovesNothing(t *testing.T) {
	for _, kind := range []string{"file", "directory", "dangling", "hidden"} {
		t.Run(kind, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			files := []string{"a.jpg", filepath.Join(".thumbs", "cached.jpg"),
				filepath.Join(".state", "fetcher.json"), filepath.Join(".state", "history.json"), filepath.Join(".state", "archive.json")}
			for _, name := range files {
				writeMigrationFile(t, filepath.Join(root, name), name)
			}
			target := filepath.Join(outside, "target")
			switch kind {
			case "directory":
				if err := os.Mkdir(target, 0o755); err != nil {
					t.Fatal(err)
				}
			case "file", "hidden":
				writeMigrationFile(t, target, "linked image")
			}
			name := "z.JPEG"
			if kind == "hidden" {
				name = ".hidden.jpg"
			}
			link := filepath.Join(root, name)
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(root, time.Now()); err == nil ||
				!strings.Contains(err.Error(), link) || !strings.Contains(err.Error(), "manually") {
				t.Fatalf("symlink error = %v, want path %s and manual handling instruction", err, link)
			}
			for _, name := range files {
				assertMigrationFile(t, filepath.Join(root, name), name)
			}
			if got, err := os.Readlink(link); err != nil || got != target {
				t.Fatalf("symlink target = %q, %v; want %q", got, err, target)
			}
			assertMigrationAbsent(t, filepath.Join(root, "boards"))
			assertMigrationAbsent(t, RegistryPath(root))
		})
	}
}

func TestOpenResumesMigrationWithOnlyArchive(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(BoardDir(root, LegacyID), ".state", "archive.json")
	writeMigrationFile(t, archive, "legacy archive")
	now := time.Unix(1234, 0)
	r, err := Open(root, now)
	if err != nil {
		t.Fatal(err)
	}
	assertMigrationRegistry(t, r, now)
	loaded, err := Load(RegistryPath(root))
	if err != nil {
		t.Fatal(err)
	}
	assertMigrationRegistry(t, loaded, now)
	assertMigrationFile(t, archive, "legacy archive")
	assertMigrationAbsent(t, filepath.Join(root, ".state", "archive.json"))
}

func TestOpenWithoutLegacyData(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"notes.txt", filepath.Join(".state", "cookie.json"), filepath.Join(".state", "notices.json")} {
		writeMigrationFile(t, filepath.Join(root, name), "keep")
	}
	r, err := Open(root, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	st := r.Snapshot()
	if st.Version != Version || st.LegacyBoardID != "" || len(st.Boards) != 0 || len(st.Archives) != 0 {
		t.Fatalf("unexpected empty registry: %+v", st)
	}
	if _, err := Load(RegistryPath(root)); err != nil {
		t.Fatal(err)
	}
	assertMigrationAbsent(t, filepath.Join(root, "boards"))
}

func TestOpenExistingRegistryDoesNotMigrate(t *testing.T) {
	root := t.TempDir()
	want := testState()
	if _, err := Create(RegistryPath(root), want); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(RegistryPath(root))
	if err != nil {
		t.Fatal(err)
	}
	writeMigrationFile(t, filepath.Join(root, "old.jpg"), "legacy image")
	// Existing registries bypass even the migration directory checks.
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "boards")); err != nil {
		t.Fatal(err)
	}
	r, err := Open(root, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Snapshot(), want) {
		t.Fatalf("registry changed: %+v", r.Snapshot())
	}
	assertMigrationFile(t, RegistryPath(root), string(before))
	assertMigrationFile(t, filepath.Join(root, "old.jpg"), "legacy image")
	assertMigrationAbsent(t, BoardDir(root, LegacyID))
}

func TestOpenInvalidRegistryDoesNotMigrate(t *testing.T) {
	for _, kind := range []string{"broken JSON", "invalid version", "read error", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := RegistryPath(root)
			writeMigrationFile(t, filepath.Join(root, "old.jpg"), "legacy image")
			switch kind {
			case "broken JSON":
				writeMigrationFile(t, path, "{")
			case "invalid version":
				writeMigrationFile(t, path, `{"version":2,"boards":[],"archives":[]}`)
			case "read error":
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatal(err)
				}
			case "dangling symlink":
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, "missing.json"), path); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Open(root, time.Now()); err == nil {
				t.Fatal("Open succeeded for an invalid registry")
			}
			assertMigrationFile(t, filepath.Join(root, "old.jpg"), "legacy image")
			assertMigrationAbsent(t, filepath.Join(root, "boards"))
			if kind == "broken JSON" {
				assertMigrationFile(t, path, "{")
			}
		})
	}
}

func TestOpenRejectsMigrationDirectorySymlinks(t *testing.T) {
	for _, name := range []string{"boards", filepath.Join("boards", LegacyID)} {
		for _, dangling := range []bool{false, true} {
			t.Run(name+"/"+map[bool]string{false: "existing", true: "dangling"}[dangling], func(t *testing.T) {
				root, outside := t.TempDir(), t.TempDir()
				writeMigrationFile(t, filepath.Join(root, "old.jpg"), "legacy image")
				link := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
					t.Fatal(err)
				}
				target := outside
				if dangling {
					target = filepath.Join(outside, "missing")
				}
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
				if _, err := Open(root, time.Now()); err == nil || !strings.Contains(err.Error(), link) {
					t.Fatalf("symlink error = %v, want path %s", err, link)
				}
				assertMigrationFile(t, filepath.Join(root, "old.jpg"), "legacy image")
				assertMigrationAbsent(t, RegistryPath(root))
				entries, err := os.ReadDir(outside)
				if err != nil || len(entries) != 0 {
					t.Fatalf("symlink target changed: %v, %v", entries, err)
				}
			})
		}
	}
}

func writeMigrationFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertMigrationFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Fatalf("%s = %q, %v; want %q", path, data, err, want)
	}
}

func assertMigrationAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s exists or cannot be checked: %v", path, err)
	}
}

func assertMigrationRegistry(t *testing.T, r *Registry, now time.Time) {
	t.Helper()
	st := r.Snapshot()
	if st.Version != Version || st.LegacyBoardID != LegacyID || len(st.Boards) != 1 || len(st.Archives) != 0 {
		t.Fatalf("unexpected migrated registry: %+v", st)
	}
	b := st.Boards[0]
	if b.ID != LegacyID || b.Name != "默认画板" || b.URL != "" || b.Lifecycle != Active ||
		!b.Created.Equal(now) || b.Created.Location() != time.UTC {
		t.Fatalf("unexpected legacy board: %+v", b)
	}
}
