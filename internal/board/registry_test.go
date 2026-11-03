package board

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func testState() State {
	return State{
		Version:       Version,
		LegacyBoardID: LegacyID,
		Boards: []Board{{
			ID:        LegacyID,
			Settings:  Settings{Name: "Main", URL: "https://example.com"},
			Lifecycle: Active,
			Created:   time.Date(2026, 11, 2, 1, 2, 3, 0, time.UTC),
		}},
		Archives: []Archive{{
			ID:        "a-0123456789abcdef",
			BoardID:   LegacyID,
			BoardName: "Main",
			File:      "main.tar.gz",
			Images:    10,
			Size:      1024,
			Created:   time.Date(2026, 11, 2, 4, 5, 6, 0, time.UTC),
		}},
	}
}

func TestLoadRejectsMissingAndInvalidFiles(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.json")
	if _, err := Load(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file error = %v", err)
	}

	tests := []struct {
		name string
		data string
	}{
		{"broken JSON", `{`},
		{"unknown version", `{"version":2,"boards":[],"archives":[]}`},
		{"duplicate board ID", `{"version":1,"boards":[{"id":"default","name":"one","lifecycle":"active","created":"2026-01-01T00:00:00Z"},{"id":"default","name":"two","lifecycle":"archived","created":"2026-01-01T00:00:00Z"}],"archives":[]}`},
		{"bad board ID", `{"version":1,"boards":[{"id":"bad","name":"one","lifecycle":"active","created":"2026-01-01T00:00:00Z"}],"archives":[]}`},
		{"blank board name", `{"version":1,"boards":[{"id":"default","name":"  ","lifecycle":"active","created":"2026-01-01T00:00:00Z"}],"archives":[]}`},
		{"bad lifecycle", `{"version":1,"boards":[{"id":"default","name":"one","lifecycle":"gone","created":"2026-01-01T00:00:00Z"}],"archives":[]}`},
		{"duplicate archive ID", `{"version":1,"boards":[],"archives":[{"id":"a-0123456789abcdef","file":"one.zip","created":"2026-01-01T00:00:00Z"},{"id":"a-0123456789abcdef","file":"two.zip","created":"2026-01-01T00:00:00Z"}]}`},
		{"bad archive ID", `{"version":1,"boards":[],"archives":[{"id":"bad","file":"one.zip","created":"2026-01-01T00:00:00Z"}]}`},
		{"archive slash", `{"version":1,"boards":[],"archives":[{"id":"a-0123456789abcdef","file":"dir/file.zip","created":"2026-01-01T00:00:00Z"}]}`},
		{"archive backslash", `{"version":1,"boards":[],"archives":[{"id":"a-0123456789abcdef","file":"dir\\file.zip","created":"2026-01-01T00:00:00Z"}]}`},
		{"archive dot dot", `{"version":1,"boards":[],"archives":[{"id":"a-0123456789abcdef","file":"file..zip","created":"2026-01-01T00:00:00Z"}]}`},
		{"archive empty", `{"version":1,"boards":[],"archives":[{"id":"a-0123456789abcdef","file":"","created":"2026-01-01T00:00:00Z"}]}`},
		{"archive dot", `{"version":1,"boards":[],"archives":[{"id":"a-0123456789abcdef","file":".","created":"2026-01-01T00:00:00Z"}]}`},
		{"bad legacy board ID", `{"version":1,"legacy_board_id":"bad","boards":[],"archives":[]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "boards.json")
			if err := os.WriteFile(path, []byte(tc.data), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("Load succeeded")
			}
		})
	}
}

func TestCreateRejectsExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "boards.json")
	if err := os.WriteFile(path, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(path, testState()); err == nil {
		t.Fatal("Create succeeded for an existing file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep me" {
		t.Fatalf("existing file was changed: %q", data)
	}
}

func TestRegistryAcceptsKnownLifecyclesAndLegacyEmptyURL(t *testing.T) {
	for _, lifecycle := range []Lifecycle{Active, Archiving, Archived, Deleting} {
		t.Run(string(lifecycle), func(t *testing.T) {
			st := testState()
			st.Boards[0].URL = ""
			st.Boards[0].Lifecycle = lifecycle
			if _, err := Create(filepath.Join(t.TempDir(), "boards.json"), st); err != nil {
				t.Fatalf("Create with lifecycle %q and empty URL: %v", lifecycle, err)
			}
		})
	}
}

func TestCreateUpdateAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "boards.json")
	want := testState()
	r, err := Create(path, want)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := r.Board(LegacyID); !ok || got.Name != "Main" {
		t.Fatalf("Board(default) = %+v, %v", got, ok)
	}
	if _, ok := r.Board("b-ffffffffffffffff"); ok {
		t.Fatal("unknown board was found")
	}
	if err := r.Update(func(st *State) error {
		st.Boards[0].Name = "Updated"
		st.Archives = append(st.Archives, Archive{ID: "a-fedcba9876543210", File: "updated.zip"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Snapshot(); !reflect.DeepEqual(got, r.Snapshot()) || got.Boards[0].Name != "Updated" || len(got.Archives) != 2 {
		t.Fatalf("reloaded state: %+v", got)
	}
}

func TestUpdateFailuresKeepMemoryAndDiskUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "boards.json")
	r, err := Create(path, testState())
	if err != nil {
		t.Fatal(err)
	}
	want := r.Snapshot()
	updateErr := errors.New("stop")
	if err := r.Update(func(st *State) error {
		st.Boards[0].Name = "changed"
		return updateErr
	}); !errors.Is(err, updateErr) {
		t.Fatalf("callback error = %v", err)
	}
	assertStateUnchanged(t, r, path, want)

	if err := r.Update(func(st *State) error {
		st.Boards[0].Lifecycle = Lifecycle("invalid")
		return nil
	}); err == nil {
		t.Fatal("invalid update succeeded")
	}
	assertStateUnchanged(t, r, path, want)

	blocker := filepath.Join(t.TempDir(), "ordinary-file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.path = filepath.Join(blocker, "boards.json")
	if err := r.Update(func(st *State) error {
		st.Boards[0].Name = "changed"
		return nil
	}); err == nil {
		t.Fatal("unwritable update succeeded")
	}
	if got := r.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("memory changed after write failure: %+v", got)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("disk changed after write failure: %+v", got)
	}
}

func TestUpdateDoesNotRetainCallbackState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "boards.json")
	r, err := Create(path, testState())
	if err != nil {
		t.Fatal(err)
	}
	var retained *State
	if err := r.Update(func(st *State) error {
		st.Boards[0].Name = "Updated"
		retained = st
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := r.Snapshot()
	retained.Boards[0].Name = "mutated"
	retained.Archives[0].File = "mutated.zip"
	assertStateUnchanged(t, r, path, want)
}

func TestRegistryRejectsInvalidSyncSettings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*Settings)
		code   string
	}{
		{"schedule", func(s *Settings) { s.Schedule = "sometimes" }, "invalid_schedule"},
		{"equal window", func(s *Settings) {
			s.SyncFrom = time.Unix(10, 0)
			s.SyncUntil = s.SyncFrom
		}, "invalid_window"},
		{"reversed window", func(s *Settings) {
			s.SyncFrom = time.Unix(11, 0)
			s.SyncUntil = time.Unix(10, 0)
		}, "invalid_window"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "boards.json")
			r, err := Create(path, testState())
			if err != nil {
				t.Fatal(err)
			}
			want := r.Snapshot()
			err = r.Update(func(st *State) error {
				tc.modify(&st.Boards[0].Settings)
				return nil
			})
			var invalid InvalidError
			if !errors.As(err, &invalid) || invalid.Code != tc.code {
				t.Fatalf("Update error = %v, want %q", err, tc.code)
			}
			assertStateUnchanged(t, r, path, want)

			tc.modify(&want.Boards[0].Settings)
			data, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); !errors.As(err, &invalid) || invalid.Code != tc.code {
				t.Fatalf("Load error = %v, want %q", err, tc.code)
			}
		})
	}
}

func assertStateUnchanged(t *testing.T, r *Registry, path string, want State) {
	t.Helper()
	if got := r.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("memory changed: %+v", got)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("disk changed: %+v", got)
	}
}

func TestSnapshotIsDeepCopyAndUsesNonNilSlices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "boards.json")
	r, err := Create(path, testState())
	if err != nil {
		t.Fatal(err)
	}
	a := r.Snapshot()
	b := r.Snapshot()
	a.Boards[0].Name = "mutated"
	a.Archives[0].File = "mutated.zip"
	if b.Boards[0].Name != "Main" || b.Archives[0].File != "main.tar.gz" {
		t.Fatalf("snapshots share storage: %+v", b)
	}
	if got, _ := r.Board(LegacyID); got.Name != "Main" {
		t.Fatalf("snapshot changed registry: %+v", got)
	}

	emptyPath := filepath.Join(t.TempDir(), "empty.json")
	empty, err := Create(emptyPath, State{Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	got := empty.Snapshot()
	if got.Boards == nil || got.Archives == nil {
		t.Fatalf("nil slices: %+v", got)
	}
	loaded, err := Load(emptyPath)
	if err != nil {
		t.Fatal(err)
	}
	got = loaded.Snapshot()
	if got.Boards == nil || got.Archives == nil {
		t.Fatalf("nil slices after Load: %+v", got)
	}
}
