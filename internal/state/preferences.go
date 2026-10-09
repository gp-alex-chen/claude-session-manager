package state

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

var preferenceKeys = map[string]bool{
	"ui-theme": true, "term-theme": true, "term-font-size": true,
	"terminal-layout-mode": true, "terminal-layout-split-ratios": true,
	"update-last-check-date":  true,
	"session-provider-filter": true,
}

// Validate renderer settings before storing them in the shared JSON document.
func normalizePreference(key, value string) (string, bool) {
	if len(value) > 64<<10 {
		return "", false
	}
	switch key {
	case "session-provider-filter":
		var ids []string
		if json.Unmarshal([]byte(value), &ids) != nil || len(ids) == 0 || len(ids) > 32 {
			return "", false
		}
		validID := regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
		unique := make(map[string]bool)
		for _, id := range ids {
			if !validID.MatchString(id) {
				return "", false
			}
			unique[id] = true
		}
		ids = make([]string, 0, len(unique))
		for id := range unique {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		data, _ := json.Marshal(ids)
		return string(data), true
	case "ui-theme":
		return value, value == "light" || value == "dark"
	case "term-theme":
		return value, map[string]bool{"claude": true, "dracula": true, "onedark": true, "solarized": true, "nord": true, "solarizedlight": true, "onelight": true, "githublight": true}[value]
	case "term-font-size":
		n, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return "", false
		}
		return strconv.Itoa(int(math.Max(10, math.Min(24, math.Floor(n+0.5))))), true
	case "terminal-layout-mode":
		return value, map[string]bool{"single": true, "split-rows-2": true, "split-cols-2": true, "split-main-left-3": true, "grid-2x2": true}[value]
	case "terminal-layout-split-ratios":
		var ratios map[string]map[string]struct {
			First float64 `json:"first"`
		}
		if json.Unmarshal([]byte(value), &ratios) != nil || ratios == nil {
			return "", false
		}
		for mode, axes := range ratios {
			if _, ok := normalizePreference("terminal-layout-mode", mode); !ok {
				delete(ratios, mode)
				continue
			}
			for axis, pair := range axes {
				if axis != "vertical" && axis != "horizontal" {
					delete(axes, axis)
					continue
				}
				pair.First = math.Max(0.16, math.Min(0.84, pair.First))
				axes[axis] = pair
			}
		}
		data, _ := json.Marshal(ratios)
		return string(data), true
	case "update-last-check-date":
		_, err := time.Parse("2006-01-02", value)
		return value, err == nil
	}
	return "", false
}

func (s *Store) withPreferenceLock(fn func() error) error {
	return s.withDocumentLock(".preferences.lock", fn)
}

func (s *Store) withDocumentLock(name string, fn func() error) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	release, err := lockProjectsFile(filepath.Join(s.dir, name))
	if err != nil {
		return err
	}
	defer release()
	return fn()
}

func savePreferenceValues(path string, values map[string]string) error {
	data, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return atomicWrite(path, data)
}

func (s *Store) Preferences() (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.preferencesLocked()
}

func (s *Store) preferencesLocked() (map[string]string, error) {
	values := map[string]string{}
	_, err := loadJSON(filepath.Join(s.dir, "preferences.json"), &values)
	if values == nil {
		values = map[string]string{}
	}
	return values, err
}

// Patch one key, preserving other keys changed by another application instance.
func (s *Store) SetPreference(key, value string) error {
	normalized, valid := normalizePreference(key, value)
	if !preferenceKeys[key] || !valid {
		return errors.New("invalid application preference")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.withPreferenceLock(func() error {
		values, err := s.preferencesLocked()
		if err != nil {
			return err
		}
		values[key] = normalized
		return savePreferenceValues(filepath.Join(s.dir, "preferences.json"), values)
	})
}
