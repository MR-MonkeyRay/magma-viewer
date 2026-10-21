package notice

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSavePublishAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "notices.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }

	d, err := s.Save("", " 维护 ", "周六\r\n停机\n\n", false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "维护" || d.Body != "周六\n停机" || d.IsPublished() || !s.Latest().IsZero() {
		t.Fatalf("draft: %+v", d)
	}

	clock = clock.Add(time.Hour)
	p, err := s.Save(d.ID, "维护", "周六停机", true)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Published.Equal(clock) || !s.Latest().Equal(clock) {
		t.Fatalf("published at %v, latest %v", p.Published, s.Latest())
	}

	// Editing a published notice keeps its time so visitors are not alerted again.
	clock = clock.Add(time.Hour)
	if e, _ := s.Save(d.ID, "维护（更正）", "周日停机", true); !e.Published.Equal(p.Published) || !e.Updated.Equal(clock) {
		t.Fatalf("edit: %+v", e)
	}

	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Published(); len(got) != 1 || got[0].Title != "维护（更正）" {
		t.Fatalf("reloaded: %+v", got)
	}

	if u, _ := r.Save(d.ID, "维护", "", false); u.IsPublished() || len(r.Published()) != 0 {
		t.Fatal("unpublish left it public")
	}
	if err := r.Delete(d.ID); err != nil || len(r.All()) != 0 {
		t.Fatalf("delete: %v", err)
	}
	if err := r.Delete(d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestSaveRejects(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "notices.json"))
	var invalid InvalidError
	for _, tc := range []struct{ title, body string }{
		{"", "x"},
		{strings.Repeat("字", MaxTitle+1), ""},
		{"ok", strings.Repeat("字", MaxBody+1)},
	} {
		if _, err := s.Save("", tc.title, tc.body, true); !errors.As(err, &invalid) {
			t.Errorf("title %d chars, body %d chars: %v", len([]rune(tc.title)), len([]rune(tc.body)), err)
		}
	}
	if _, err := s.Save("nope", "t", "", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	if len(s.All()) != 0 {
		t.Fatal("rejected saves were kept")
	}
}
