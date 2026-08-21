// Package state persists application-owned JSON while retaining the original
// favorites.json/open-sessions.json/settings.json formats.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

type FavState struct {
	Ids     []string          `json:"ids"`
	Aliases map[string]string `json:"aliases"`
	Hidden  []string          `json:"hidden"`
}

type Store struct {
	dir string
	mu  sync.Mutex
}

func NewStore(dir string) *Store { return &Store{dir: dir} }

func DefaultDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

var defaultStore = NewStore(DefaultDir())

func Default() *Store   { return defaultStore }
func FavPath() string   { return filepath.Join(defaultStore.dir, "favorites.json") }
func OpenPath() string  { return filepath.Join(defaultStore.dir, "open-sessions.json") }
func ShellPath() string { return filepath.Join(defaultStore.dir, "settings.json") }

func (s *Store) favPath() string   { return filepath.Join(s.dir, "favorites.json") }
func (s *Store) openPath() string  { return filepath.Join(s.dir, "open-sessions.json") }
func (s *Store) shellPath() string { return filepath.Join(s.dir, "settings.json") }

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".state-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0o644); err == nil {
		_, err = f.Write(data)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return atomicReplace(tmp, path)
}

func emptyState() *FavState { return &FavState{Aliases: map[string]string{}} }

func normalizeState(st *FavState) *FavState {
	if st == nil {
		return emptyState()
	}
	if st.Aliases == nil {
		st.Aliases = map[string]string{}
	}
	return st
}

func loadJSON(path string, dst any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	return nil
}

func (s *Store) loadLocked() (*FavState, error) {
	st := emptyState()
	if err := loadJSON(s.favPath(), st); err != nil {
		return st, err
	}
	return normalizeState(st), nil
}

func encodeFavorites(st *FavState) ([]byte, error) {
	st = normalizeState(st)
	return json.Marshal(map[string]any{"ids": st.Ids, "aliases": st.Aliases, "hidden": st.Hidden})
}

func (s *Store) saveLocked(st *FavState) error {
	b, err := encodeFavorites(st)
	if err != nil {
		return err
	}
	return atomicWrite(s.favPath(), b)
}

// Load returns a safe default for absent, corrupt, or unreadable data and the
// diagnostic error separately so callers can log it without crashing startup.
func (s *Store) Load() (*FavState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

// Save remains available for callers that replace the whole compatible state.
func (s *Store) Save(st *FavState) error {
	if st == nil {
		return errors.New("nil state")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(st)
}

func (s *Store) SetAlias(id, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		// Preserve the safe state but do not overwrite a corrupt file silently.
		return err
	}
	if name == "" {
		delete(st.Aliases, id)
	} else {
		st.Aliases[id] = name
	}
	return s.saveLocked(st)
}

func (s *Store) SetHidden(id string, hidden bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if hidden {
		seen := false
		out := st.Hidden[:0]
		for _, item := range st.Hidden {
			if item == id {
				if seen {
					continue
				}
				seen = true
			}
			out = append(out, item)
		}
		if !seen {
			out = append(out, id)
		}
		st.Hidden = out
	} else {
		out := st.Hidden[:0]
		for _, item := range st.Hidden {
			if item != id {
				out = append(out, item)
			}
		}
		st.Hidden = out
	}
	return s.saveLocked(st)
}

func (s *Store) SaveOpen(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copyIDs := append([]string(nil), ids...)
	sort.Strings(copyIDs)
	b, err := json.Marshal(map[string][]string{"ids": copyIDs})
	if err != nil {
		return err
	}
	return atomicWrite(s.openPath(), b)
}

func (s *Store) LoadOpen() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var doc struct {
		Ids []string `json:"ids"`
	}
	if err := loadJSON(s.openPath(), &doc); err != nil {
		return nil, err
	}
	return doc.Ids, nil
}

func (s *Store) SetShell(name string) error {
	if name != "cmd" && name != "pwsh" {
		name = "cmd"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(map[string]string{"shell": name})
	if err != nil {
		return err
	}
	return atomicWrite(s.shellPath(), b)
}

func (s *Store) Shell() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var doc struct {
		Shell string `json:"shell"`
	}
	if err := loadJSON(s.shellPath(), &doc); err != nil {
		return "cmd", err
	}
	if doc.Shell != "cmd" && doc.Shell != "pwsh" {
		return "cmd", fmt.Errorf("invalid shell %q", doc.Shell)
	}
	return doc.Shell, nil
}

func (st *FavState) HiddenSet() map[string]bool {
	m := make(map[string]bool, len(st.Hidden))
	for _, h := range st.Hidden {
		m[h] = true
	}
	return m
}

func (st *FavState) RemoveHidden(id string) {
	out := st.Hidden[:0]
	for _, h := range st.Hidden {
		if h != id {
			out = append(out, h)
		}
	}
	st.Hidden = out
}
