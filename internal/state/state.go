// Package state persists the small amount of application-owned state.
// The JSON shapes and default location intentionally remain compatible with
// the original fyne-sidebar implementation.
package state

import (
	"encoding/json"
	"errors"
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

func (s *Store) Load() *FavState {
	st := &FavState{Aliases: map[string]string{}}
	b, err := os.ReadFile(s.favPath())
	if err != nil {
		return st
	}
	if json.Unmarshal(b, st) != nil {
		return &FavState{Aliases: map[string]string{}}
	}
	if st.Aliases == nil {
		st.Aliases = map[string]string{}
	}
	return st
}

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

func (s *Store) Save(st *FavState) error {
	if st == nil {
		return errors.New("nil state")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if st.Aliases == nil {
		st.Aliases = map[string]string{}
	}
	b, err := json.Marshal(map[string]any{"ids": st.Ids, "aliases": st.Aliases, "hidden": st.Hidden})
	if err != nil {
		return err
	}
	return atomicWrite(s.favPath(), b)
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

func (s *Store) SaveOpen(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids = append([]string(nil), ids...)
	sort.Strings(ids)
	b, err := json.Marshal(map[string][]string{"ids": ids})
	if err != nil {
		return err
	}
	return atomicWrite(s.openPath(), b)
}
func (s *Store) LoadOpen() []string {
	b, err := os.ReadFile(s.openPath())
	if err != nil {
		return nil
	}
	var j struct {
		Ids []string `json:"ids"`
	}
	if json.Unmarshal(b, &j) != nil {
		return nil
	}
	return j.Ids
}
func (s *Store) SaveShell(name string) error {
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
func (s *Store) Shell() string {
	b, err := os.ReadFile(s.shellPath())
	if err != nil {
		return "cmd"
	}
	var j struct {
		Shell string `json:"shell"`
	}
	if json.Unmarshal(b, &j) != nil || (j.Shell != "cmd" && j.Shell != "pwsh") {
		return "cmd"
	}
	return j.Shell
}

func Load() *FavState             { return defaultStore.Load() }
func Save(st *FavState) error     { return defaultStore.Save(st) }
func LoadOpen() []string          { return defaultStore.LoadOpen() }
func SaveOpen(ids []string) error { return defaultStore.SaveOpen(ids) }
func Shell() string               { return defaultStore.Shell() }
func SaveShell(name string) error { return defaultStore.SaveShell(name) }
