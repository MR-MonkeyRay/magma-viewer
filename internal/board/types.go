// Package board keeps board settings and the persisted board registry.
package board

import (
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/robfig/cron/v3"
)

type Lifecycle string

const (
	Active    Lifecycle = "active"
	Archiving Lifecycle = "archiving"
	Archived  Lifecycle = "archived"
	Deleting  Lifecycle = "deleting"
)

// LegacyID identifies the board migrated from the single-board layout.
const LegacyID = "default"

// Settings are the fields an admin edits.
type Settings struct {
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Schedule  string    `json:"schedule,omitempty"`  // empty inherits the global schedule
	SyncFrom  time.Time `json:"sync_from,omitzero"`  // inclusive
	SyncUntil time.Time `json:"sync_until,omitzero"` // exclusive
	Hidden    bool      `json:"hidden,omitempty"`    // omitted from the public list; not access control
	Sort      int       `json:"sort,omitempty"`
}

type Board struct {
	ID string `json:"id"`
	Settings
	Lifecycle Lifecycle `json:"lifecycle"`
	Created   time.Time `json:"created"`
}

type Archive struct {
	ID        string    `json:"id"`
	BoardID   string    `json:"board_id"`
	BoardName string    `json:"board_name"`
	File      string    `json:"file"` // base name inside the archives directory
	Images    int       `json:"images"`
	Size      int64     `json:"size"`
	Created   time.Time `json:"created"`
}

// InvalidError carries a stable code and a Chinese message for rejected settings.
type InvalidError struct{ Code, Msg string }

func (e InvalidError) Error() string { return e.Msg }

func (s Settings) Normalize() (Settings, error) {
	s.Name = strings.TrimSpace(s.Name)
	s.URL = strings.TrimSpace(s.URL)
	s.Schedule = strings.TrimSpace(s.Schedule)
	if s.Name == "" {
		return Settings{}, InvalidError{"name_required", "请填写画板名称"}
	}
	if utf8.RuneCountInString(s.Name) > 60 {
		return Settings{}, InvalidError{"name_too_long", "画板名称最多 60 字"}
	}
	if u, err := url.Parse(s.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Settings{}, InvalidError{"invalid_url", "画板地址必须是包含主机名的 HTTP 或 HTTPS 地址"}
	}
	s.SyncFrom = s.SyncFrom.UTC().Truncate(time.Second)
	s.SyncUntil = s.SyncUntil.UTC().Truncate(time.Second)
	if err := s.validateSync(); err != nil {
		return Settings{}, err
	}
	if s.Sort < -100000 || s.Sort > 100000 {
		return Settings{}, InvalidError{"invalid_sort", "排序值必须在 -100000 到 100000 之间"}
	}
	return s, nil
}

func (s Settings) validateSync() error {
	if s.Schedule != "" {
		if _, err := cron.ParseStandard(s.Schedule); err != nil {
			return InvalidError{"invalid_schedule", "同步频率格式无效"}
		}
	}
	if !s.SyncFrom.IsZero() && !s.SyncUntil.IsZero() && !s.SyncFrom.Before(s.SyncUntil) {
		return InvalidError{"invalid_window", "同步开始时间必须早于结束时间"}
	}
	return nil
}

func (b Board) InWindow(now time.Time) bool {
	return (b.SyncFrom.IsZero() || !now.Before(b.SyncFrom)) &&
		(b.SyncUntil.IsZero() || now.Before(b.SyncUntil))
}

func (b Board) SyncState(now time.Time, paused bool) string {
	switch {
	case paused:
		return "paused"
	case !b.SyncFrom.IsZero() && now.Before(b.SyncFrom):
		return "pending"
	case !b.SyncUntil.IsZero() && !now.Before(b.SyncUntil):
		return "ended"
	default:
		return "syncing"
	}
}

func NewID() (string, error)        { return newID("b-") }
func NewArchiveID() (string, error) { return newID("a-") }

func newID(prefix string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b[:]), nil
}

func ValidID(id string) bool        { return id == LegacyID || validID(id, "b-") }
func ValidArchiveID(id string) bool { return validID(id, "a-") }

func validID(id, prefix string) bool {
	if len(id) != len(prefix)+16 || !strings.HasPrefix(id, prefix) {
		return false
	}
	for _, c := range id[len(prefix):] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
