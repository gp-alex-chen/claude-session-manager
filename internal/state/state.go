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
	"strings"
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

func (s *Store) favPath() string      { return filepath.Join(s.dir, "favorites.json") }
func (s *Store) openPath() string     { return filepath.Join(s.dir, "open-sessions.json") }
func (s *Store) shellPath() string    { return filepath.Join(s.dir, "settings.json") }
func (s *Store) projectsPath() string { return filepath.Join(s.dir, "projects.json") }

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

func loadJSON(path string, dst any) (bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return true, fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	return true, nil
}

func (s *Store) loadLocked() (*FavState, error) {
	st := emptyState()
	_, err := loadJSON(s.favPath(), st)
	if err != nil {
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
	if err != nil {
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
	if err != nil {
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
	found, err := loadJSON(s.openPath(), &doc)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return doc.Ids, nil
}

type projectsDocument struct {
	Dirs      []string `json:"dirs"`
	Favorites []string `json:"favorites,omitempty"`
}

func normalizeProjectDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", errors.New("项目目录不能为空")
	}
	return filepath.Abs(filepath.Clean(dir))
}

func normalizeProjectDirs(dirs []string) ([]string, error) {
	result := make([]string, 0, len(dirs))
	seen := make(map[string]struct{}, len(dirs))
	for _, dir := range dirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		normalized, err := normalizeProjectDir(dir)
		if err != nil {
			return nil, err
		}
		key := projectPathKey(normalized)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, normalized)
	}
	return result, nil
}

func (s *Store) withProjectsFileLock(fn func() error) error {
	release, err := lockProjectsFile(filepath.Join(s.dir, ".projects.lock"))
	if err != nil {
		return err
	}
	result := fn()
	if releaseErr := release(); result == nil {
		return releaseErr
	}
	return result
}

func (s *Store) loadProjectsDocumentLocked() (projectsDocument, error) {
	doc := projectsDocument{Dirs: []string{}, Favorites: []string{}}
	found, err := loadJSON(s.projectsPath(), &doc)
	if err != nil {
		return projectsDocument{Dirs: []string{}, Favorites: []string{}}, err
	}
	if !found {
		return doc, nil
	}
	dirs, err := normalizeProjectDirs(doc.Dirs)
	if err != nil {
		return projectsDocument{Dirs: []string{}, Favorites: []string{}}, err
	}
	favorites, err := normalizeProjectDirs(doc.Favorites)
	if err != nil {
		return projectsDocument{Dirs: []string{}, Favorites: []string{}}, err
	}
	return projectsDocument{Dirs: dirs, Favorites: favorites}, nil
}

func (s *Store) loadProjectsLocked() ([]string, error) {
	doc, err := s.loadProjectsDocumentLocked()
	return doc.Dirs, err
}

func saveProjectsDocumentLocked(path string, doc projectsDocument) error {
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return atomicWrite(path, b)
}

func (s *Store) LoadProjects() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadProjectsLocked()
}

func (s *Store) SaveProjects(dirs []string) error {
	normalized, err := normalizeProjectDirs(dirs)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.withProjectsFileLock(func() error {
		doc, err := s.loadProjectsDocumentLocked()
		if err != nil {
			return err
		}
		doc.Dirs = normalized
		return saveProjectsDocumentLocked(s.projectsPath(), doc)
	})
}

func (s *Store) AddProject(dir string) error {
	normalized, err := normalizeProjectDir(dir)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.withProjectsFileLock(func() error {
		doc, err := s.loadProjectsDocumentLocked()
		if err != nil {
			return err
		}
		for _, existing := range doc.Dirs {
			if sameProjectPath(existing, normalized) {
				return nil
			}
		}
		doc.Dirs = append(doc.Dirs, normalized)
		return saveProjectsDocumentLocked(s.projectsPath(), doc)
	})
}

func (s *Store) DeleteProject(dir string) error {
	normalized, err := normalizeProjectDir(dir)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.withProjectsFileLock(func() error {
		doc, err := s.loadProjectsDocumentLocked()
		if err != nil {
			return err
		}
		filtered := doc.Dirs[:0]
		removed := false
		for _, existing := range doc.Dirs {
			if sameProjectPath(existing, normalized) {
				removed = true
				continue
			}
			filtered = append(filtered, existing)
		}
		if !removed {
			return nil
		}
		doc.Dirs = filtered
		favorites := doc.Favorites[:0]
		for _, favorite := range doc.Favorites {
			if !sameProjectPath(favorite, normalized) {
				favorites = append(favorites, favorite)
			}
		}
		doc.Favorites = favorites
		return saveProjectsDocumentLocked(s.projectsPath(), doc)
	})
}

func (s *Store) LoadProjectFavorites() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadProjectsDocumentLocked()
	if err != nil {
		return []string{}, err
	}
	return append([]string(nil), doc.Favorites...), nil
}

func (s *Store) SetProjectFavorite(dir string, favorite bool) error {
	normalized, err := normalizeProjectDir(dir)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.withProjectsFileLock(func() error {
		doc, err := s.loadProjectsDocumentLocked()
		if err != nil {
			return err
		}
		filtered := make([]string, 0, len(doc.Favorites)+1)
		for _, existing := range doc.Favorites {
			if sameProjectPath(existing, normalized) {
				continue
			}
			filtered = append(filtered, existing)
		}
		if favorite {
			doc.Favorites = append([]string{normalized}, filtered...)
		} else {
			doc.Favorites = filtered
		}
		return saveProjectsDocumentLocked(s.projectsPath(), doc)
	})
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
	found, err := loadJSON(s.shellPath(), &doc)
	if err != nil {
		return "cmd", err
	}
	if !found {
		return "cmd", nil
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
