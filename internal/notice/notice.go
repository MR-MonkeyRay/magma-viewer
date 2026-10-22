// Package notice keeps the announcements admins write for visitors, persisted
// as one JSON file.
package notice

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"magma-viewer/internal/fsutil"
)

const (
	MaxTitle = 100  // characters
	MaxBody  = 5000 // characters
	MaxCount = 200
)

var ErrNotFound = errors.New("公告不存在")

// InvalidError is a rejected title or body. Code is a stable key the page translates;
// Msg is the Chinese text, shown as is when the page does not know the code.
type InvalidError struct{ Code, Msg string }

func (e InvalidError) Error() string { return e.Msg }

type Notice struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
	Published time.Time `json:"published"` // zero while a draft
}

func (n Notice) IsPublished() bool { return !n.Published.IsZero() }

type Store struct {
	path string
	now  func() time.Time

	mu    sync.RWMutex
	items []Notice // newest created first
}

// Open loads the notices kept at path; a missing file is an empty list.
func Open(path string) (*Store, error) {
	s := &Store{path: path, now: time.Now}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.items); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	sortByCreated(s.items)
	return s, nil
}

func sortByCreated(items []Notice) {
	slices.SortStableFunc(items, func(a, b Notice) int { return b.Created.Compare(a.Created) })
}

// All lists drafts and published notices, newest created first.
func (s *Store) All() []Notice {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Notice{}, s.items...) // never nil, so an empty list encodes as []
}

// Published lists what visitors see, newest published first.
func (s *Store) Published() []Notice {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Notice{}
	for _, n := range s.items {
		if n.IsPublished() {
			out = append(out, n)
		}
	}
	slices.SortStableFunc(out, func(a, b Notice) int { return b.Published.Compare(a.Published) })
	return out
}

// Latest is when the newest published notice went out, zero when there is none.
func (s *Store) Latest() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var t time.Time
	for _, n := range s.items {
		if n.Published.After(t) {
			t = n.Published
		}
	}
	return t
}

// Save creates a notice when id is empty, otherwise replaces that notice's text.
// publish keeps an already published notice's time, so fixing a typo does not
// announce it again; publish=false turns the notice back into a draft.
func (s *Store) Save(id, title, body string, publish bool) (Notice, error) {
	title = strings.TrimSpace(title)
	body = strings.TrimRight(strings.ReplaceAll(body, "\r\n", "\n"), " \t\n")
	switch {
	case title == "":
		return Notice{}, InvalidError{"title_required", "请填写标题"}
	case utf8.RuneCountInString(title) > MaxTitle:
		return Notice{}, InvalidError{"title_too_long", fmt.Sprintf("标题最多 %d 字", MaxTitle)}
	case utf8.RuneCountInString(body) > MaxBody:
		return Notice{}, InvalidError{"body_too_long", fmt.Sprintf("内容最多 %d 字", MaxBody)}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Millisecond precision so the times survive a round trip through JavaScript dates unchanged.
	now := s.now().UTC().Truncate(time.Millisecond)
	items := slices.Clone(s.items)
	i := 0
	if id == "" {
		if len(items) >= MaxCount {
			return Notice{}, InvalidError{"too_many_notices", fmt.Sprintf("最多保留 %d 条公告，请先删除旧公告", MaxCount)}
		}
		items = slices.Insert(items, 0, Notice{ID: newID(), Created: now})
	} else if i = slices.IndexFunc(items, func(n Notice) bool { return n.ID == id }); i < 0 {
		return Notice{}, ErrNotFound
	}
	n := &items[i]
	n.Title, n.Body, n.Updated = title, body, now
	switch {
	case !publish:
		n.Published = time.Time{}
	case !n.IsPublished():
		n.Published = now
	}
	if err := s.write(items); err != nil {
		return Notice{}, err
	}
	s.items = items
	return *n, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.items, func(n Notice) bool { return n.ID == id })
	if i < 0 {
		return ErrNotFound
	}
	items := slices.Delete(slices.Clone(s.items), i, i+1)
	if err := s.write(items); err != nil {
		return err
	}
	s.items = items
	return nil
}

// write replaces the file atomically; the caller holds mu.
func (s *Store) write(items []Notice) error {
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFile(s.path, data, 0o644)
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
