package board

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSettingsNormalize(t *testing.T) {
	zone := time.FixedZone("UTC+8", 8*60*60)
	from := time.Date(2026, 11, 2, 12, 34, 56, 789000000, zone)
	until := from.Add(2*time.Hour + 999*time.Millisecond)
	s := Settings{
		Name:      "  画板 ",
		URL:       " https://example.com/board ",
		Schedule:  " 0 * * * * ",
		SyncFrom:  from,
		SyncUntil: until,
		Sort:      100000,
	}

	got, err := s.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "画板" || got.URL != "https://example.com/board" || got.Schedule != "0 * * * *" {
		t.Fatalf("trimmed settings: %+v", got)
	}
	if got.SyncFrom.Location() != time.UTC || got.SyncUntil.Location() != time.UTC {
		t.Fatalf("times are not UTC: %v, %v", got.SyncFrom, got.SyncUntil)
	}
	if got.SyncFrom.Nanosecond() != 0 || got.SyncUntil.Nanosecond() != 0 {
		t.Fatalf("times were not truncated to seconds: %v, %v", got.SyncFrom, got.SyncUntil)
	}
	if want := from.UTC().Truncate(time.Second); !got.SyncFrom.Equal(want) {
		t.Fatalf("SyncFrom = %v, want %v", got.SyncFrom, want)
	}
	if s.Name != "  画板 " || s.URL != " https://example.com/board " {
		t.Fatalf("Normalize modified its receiver: %+v", s)
	}

	if _, err := (Settings{Name: strings.Repeat("字", 60), URL: "http://example.com", Sort: -100000}).Normalize(); err != nil {
		t.Fatalf("valid boundaries: %v", err)
	}
	blank, err := (Settings{Name: "board", URL: "http://example.com", Schedule: "  "}).Normalize()
	if err != nil {
		t.Fatalf("blank inherited schedule: %v", err)
	}
	if blank.Schedule != "" || !blank.SyncFrom.IsZero() || !blank.SyncUntil.IsZero() {
		t.Fatalf("blank schedule or zero times changed: %+v", blank)
	}
}

func TestSettingsNormalizeRejects(t *testing.T) {
	tests := []struct {
		name string
		in   Settings
		code string
	}{
		{"missing name", Settings{URL: "https://example.com"}, "name_required"},
		{"long name", Settings{Name: strings.Repeat("字", 61), URL: "https://example.com"}, "name_too_long"},
		{"missing URL", Settings{Name: "board"}, "invalid_url"},
		{"URL without host", Settings{Name: "board", URL: "https:///path"}, "invalid_url"},
		{"wrong URL scheme", Settings{Name: "board", URL: "ftp://example.com"}, "invalid_url"},
		{"bad schedule", Settings{Name: "board", URL: "https://example.com", Schedule: "sometimes"}, "invalid_schedule"},
		{"equal window", Settings{Name: "board", URL: "https://example.com", SyncFrom: time.Unix(10, 0), SyncUntil: time.Unix(10, 0)}, "invalid_window"},
		{"window collapses after truncation", Settings{Name: "board", URL: "https://example.com", SyncFrom: time.Unix(10, 100), SyncUntil: time.Unix(10, 900)}, "invalid_window"},
		{"reversed window", Settings{Name: "board", URL: "https://example.com", SyncFrom: time.Unix(11, 0), SyncUntil: time.Unix(10, 0)}, "invalid_window"},
		{"sort too small", Settings{Name: "board", URL: "https://example.com", Sort: -100001}, "invalid_sort"},
		{"sort too large", Settings{Name: "board", URL: "https://example.com", Sort: 100001}, "invalid_sort"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.in.Normalize()
			var invalid InvalidError
			if !errors.As(err, &invalid) || invalid.Code != tc.code || invalid.Msg == "" {
				t.Fatalf("error = %#v, want InvalidError code %q with a message", err, tc.code)
			}
		})
	}
}

func TestBoardInWindow(t *testing.T) {
	from := time.Unix(100, 0)
	until := time.Unix(200, 0)
	tests := []struct {
		name  string
		board Board
		now   time.Time
		want  bool
	}{
		{"before", Board{Settings: Settings{SyncFrom: from, SyncUntil: until}}, from.Add(-time.Second), false},
		{"at from", Board{Settings: Settings{SyncFrom: from, SyncUntil: until}}, from, true},
		{"at until", Board{Settings: Settings{SyncFrom: from, SyncUntil: until}}, until, false},
		{"after", Board{Settings: Settings{SyncFrom: from, SyncUntil: until}}, until.Add(time.Second), false},
		{"no lower boundary", Board{Settings: Settings{SyncUntil: until}}, time.Time{}, true},
		{"no upper boundary", Board{Settings: Settings{SyncFrom: from}}, until.Add(time.Hour), true},
		{"no boundaries", Board{}, time.Now(), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.board.InWindow(tc.now); got != tc.want {
				t.Fatalf("InWindow(%v) = %v, want %v", tc.now, got, tc.want)
			}
		})
	}
}

func TestBoardSyncState(t *testing.T) {
	from := time.Unix(100, 0)
	until := time.Unix(200, 0)
	b := Board{Settings: Settings{SyncFrom: from, SyncUntil: until}}
	for _, tc := range []struct {
		name   string
		now    time.Time
		paused bool
		want   string
	}{
		{"paused takes priority", from.Add(-time.Hour), true, "paused"},
		{"pending", from.Add(-time.Nanosecond), false, "pending"},
		{"syncing", from, false, "syncing"},
		{"ended", until, false, "ended"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := b.SyncState(tc.now, tc.paused); got != tc.want {
				t.Fatalf("SyncState = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIDs(t *testing.T) {
	boardID, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if !ValidID(boardID) || len(boardID) != len("b-")+16 || !ValidID(LegacyID) {
		t.Fatalf("invalid generated board ID %q", boardID)
	}
	archiveID, err := NewArchiveID()
	if err != nil {
		t.Fatal(err)
	}
	if !ValidArchiveID(archiveID) || len(archiveID) != len("a-")+16 {
		t.Fatalf("invalid generated archive ID %q", archiveID)
	}
	for _, id := range []string{"", "b-0123456789abcde", "b-0123456789abcdef0", "b-0123456789ABCDEf", "a-0123456789abcdef"} {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true", id)
		}
	}
	for _, id := range []string{"", LegacyID, "a-0123456789abcde", "a-0123456789abcdef0", "a-0123456789ABCDEf", "b-0123456789abcdef"} {
		if ValidArchiveID(id) {
			t.Errorf("ValidArchiveID(%q) = true", id)
		}
	}
}
