package scheduler

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Store struct {
	path string
	loc  *time.Location
	mu   sync.RWMutex
	list []Reservation
}

func LoadStore(path string, loc *time.Location) (*Store, error) {
	store := &Store{path: path, loc: loc}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read schedules: %w", err)
	}
	reservations, err := Parse(data, loc)
	if err != nil {
		return nil, err
	}
	store.list = reservations
	return store, nil
}

func (s *Store) List() []Reservation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Reservation(nil), s.list...)
}

func (s *Store) Add(reservation Reservation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := reservation.Validate(s.loc); err != nil {
		return err
	}
	for _, existing := range s.list {
		if existing.ID == reservation.ID {
			return fmt.Errorf("schedule %q already exists", reservation.ID)
		}
	}
	candidate := append(append([]Reservation(nil), s.list...), reservation)
	if a, b, ok := FindOverlap(candidate, s.loc); ok {
		return fmt.Errorf("overlapping reservations %q and %q", a, b)
	}
	return s.saveLocked(candidate)
}

func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate := make([]Reservation, 0, len(s.list))
	found := false
	for _, reservation := range s.list {
		if reservation.ID == id {
			found = true
			continue
		}
		candidate = append(candidate, reservation)
	}
	if !found {
		return fmt.Errorf("schedule %q not found", id)
	}
	return s.saveLocked(candidate)
}

func (s *Store) SetEnabled(id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate := append([]Reservation(nil), s.list...)
	found := false
	for i := range candidate {
		if candidate[i].ID == id {
			candidate[i].Enabled = enabled
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("schedule %q not found", id)
	}
	if a, b, ok := FindOverlap(candidate, s.loc); ok {
		return fmt.Errorf("overlapping reservations %q and %q", a, b)
	}
	return s.saveLocked(candidate)
}

func (s *Store) saveLocked(candidate []Reservation) error {
	data, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return fmt.Errorf("create schedule directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".schedules-*.json")
	if err != nil {
		return fmt.Errorf("create temporary schedule file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o640); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace schedule file: %w", err)
	}
	s.list = candidate
	return nil
}
