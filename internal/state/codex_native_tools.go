package state

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
)

var nativeToolSessionID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// This is a catalog capability cache, never authorization. No grants, runtime
// tokens, target references or credentials survive an application restart.
func (s *Store) LoadCodexNativeTools() (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadCodexNativeToolsLocked()
}
func (s *Store) loadCodexNativeToolsLocked() (map[string]string, error) {
	values := map[string]string{}
	_, err := loadJSON(filepath.Join(s.dir, "codex-native-tools.json"), &values)
	if values == nil {
		values = map[string]string{}
	}
	for id, catalog := range values {
		if !nativeToolSessionID.MatchString(id) || catalog == "" || len(catalog) > 128 {
			return nil, errors.New("invalid native tool capability cache")
		}
	}
	return values, err
}
func (s *Store) SaveCodexNativeTools(id, catalog string) error {
	if !nativeToolSessionID.MatchString(id) || catalog == "" || len(catalog) > 128 {
		return errors.New("invalid native tool capability")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.withDocumentLock(".codex-native-tools.lock", func() error {
		values, err := s.loadCodexNativeToolsLocked()
		if err != nil {
			return err
		}
		values[id] = catalog
		data, err := json.Marshal(values)
		if err != nil {
			return err
		}
		return atomicWrite(filepath.Join(s.dir, "codex-native-tools.json"), data)
	})
}
