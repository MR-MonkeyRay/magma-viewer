package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileReplacesAtomically(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "state.json")
	for _, content := range []string{"one", "two"} {
		if err := WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(p); string(got) != content {
			t.Fatalf("got %q, want %q", got, content)
		}
	}
	// The umask may only narrow the requested mode, never widen it.
	if info, _ := os.Stat(p); info.Mode().Perm()&^0o644 != 0 {
		t.Fatalf("mode %v is wider than 0644", info.Mode())
	}
	if entries, _ := os.ReadDir(filepath.Dir(p)); len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}
