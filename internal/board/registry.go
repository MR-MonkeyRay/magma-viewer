package board

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"magma-viewer/internal/fsutil"
)

const Version = 1

type State struct {
	Version       int       `json:"version"`
	LegacyBoardID string    `json:"legacy_board_id,omitempty"`
	Boards        []Board   `json:"boards"`
	Archives      []Archive `json:"archives"`
}

type Registry struct {
	path string
	mu   sync.RWMutex
	st   State
}

var ErrNotFound = errors.New("画板不存在")

// Load requires an existing, valid registry; it never rebuilds missing state.
func Load(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := validate(st); err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	return &Registry{path: path, st: clone(st)}, nil
}

// Create writes initial state and refuses to replace an existing registry.
func Create(path string, st State) (*Registry, error) {
	if _, err := os.Lstat(path); err == nil {
		return nil, fmt.Errorf("create %s: %w", path, os.ErrExist)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := validate(st); err != nil {
		return nil, err
	}
	r := &Registry{path: path, st: clone(st)}
	if err := r.write(r.st); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Registry) Snapshot() State {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return clone(r.st)
}

func (r *Registry) Board(id string) (Board, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, b := range r.st.Boards {
		if b.ID == id {
			return b, true
		}
	}
	return Board{}, false
}

// Update commits a copy only after validation and an atomic file write succeed.
func (r *Registry) Update(fn func(*State) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := clone(r.st)
	if err := fn(&st); err != nil {
		return err
	}
	if err := validate(st); err != nil {
		return err
	}
	committed := clone(st)
	if err := r.write(committed); err != nil {
		return err
	}
	r.st = committed
	return nil
}

func clone(st State) State {
	st.Boards = append([]Board{}, st.Boards...)
	st.Archives = append([]Archive{}, st.Archives...)
	return st
}

func (r *Registry) write(st State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFile(r.path, data, 0o644)
}

func validate(st State) error {
	if st.Version != Version {
		return fmt.Errorf("unsupported board registry version: %d", st.Version)
	}
	if st.LegacyBoardID != "" && !ValidID(st.LegacyBoardID) {
		return fmt.Errorf("invalid legacy board ID: %q", st.LegacyBoardID)
	}
	boards := make(map[string]bool, len(st.Boards))
	for _, b := range st.Boards {
		if !ValidID(b.ID) || boards[b.ID] {
			return fmt.Errorf("invalid or duplicate board ID: %q", b.ID)
		}
		boards[b.ID] = true
		switch b.Lifecycle {
		case Active, Archiving, Archived, Deleting:
		default:
			return fmt.Errorf("invalid board lifecycle: %q", b.Lifecycle)
		}
		if strings.TrimSpace(b.Name) == "" {
			return fmt.Errorf("board %q has an empty name", b.ID)
		}
		if err := b.validateSync(); err != nil {
			return fmt.Errorf("board %q: %w", b.ID, err)
		}
	}
	archives := make(map[string]bool, len(st.Archives))
	for _, a := range st.Archives {
		if !ValidArchiveID(a.ID) || archives[a.ID] {
			return fmt.Errorf("invalid or duplicate archive ID: %q", a.ID)
		}
		archives[a.ID] = true
		if a.File == "" || a.File == "." || strings.ContainsAny(a.File, "/\\") || strings.Contains(a.File, "..") {
			return fmt.Errorf("invalid archive file: %q", a.File)
		}
	}
	return nil
}
