// Package snapshotstate persists the agent's scheduled-snapshot state to disk
// (issue #9): the per-device cadence (set by the snapshot.config handler) and
// the per-camera next-fire timestamps (managed by the scheduler in slice 3b).
// Persisting both means an agent restart neither forgets its cadence nor resets
// the schedule — the issue's acceptance criterion.
package snapshotstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// State is the on-disk shape. Cadence is "" until the first snapshot.config
// lands (the scheduler treats "" the same as "off").
type State struct {
	Cadence  string               `json:"cadence"`
	NextFire map[string]time.Time `json:"next_fire,omitempty"`
}

// Store is a mutex-serialised read-modify-write wrapper over one JSON file. The
// file is the source of truth; the mutex serialises concurrent writers (the
// snapshot.config handler and the scheduler).
type Store struct {
	path string
	mu   sync.Mutex
}

func NewStore(path string) *Store { return &Store{path: path} }

// Load reads the current state. A missing file is the zero State, not an error
// (a fresh agent has no schedule yet).
func (s *Store) Load() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) loadLocked() (State, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read snapshot state: %w", err)
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return State{}, fmt.Errorf("parse snapshot state: %w", err)
	}
	return st, nil
}

// SetCadence updates the cadence, preserving the existing next-fire schedule.
func (s *Store) SetCadence(cadence string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return err
	}
	st.Cadence = cadence
	return s.saveLocked(st)
}

// SetNextFire records the next scheduled fire time for one camera, preserving
// the cadence and other cameras' schedules (used by the scheduler, slice 3b).
func (s *Store) SetNextFire(cameraID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return err
	}
	if st.NextFire == nil {
		st.NextFire = map[string]time.Time{}
	}
	st.NextFire[cameraID] = at
	return s.saveLocked(st)
}

func (s *Store) saveLocked(st State) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot state: %w", err)
	}
	return atomicWrite(s.path, raw)
}

// atomicWrite writes via a temp file + rename so a crash never leaves a
// half-written state file.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
