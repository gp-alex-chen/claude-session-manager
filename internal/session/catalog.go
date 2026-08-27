package session

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type fileFingerprint struct {
	size    int64
	modTime int64
}

type catalogEntry struct {
	fingerprint fileFingerprint
	session     *Session
}

// Catalog incrementally scans a Claude projects root. Directory enumeration
// still happens at refresh time, but unchanged JSONL files reuse their parsed
// Session value and closely-spaced callers reuse the complete snapshot.
type Catalog struct {
	mu            sync.Mutex
	scanMu        sync.Mutex
	root          string
	cache         map[string]catalogEntry
	snapshot      []*Session
	snapshotAt    time.Time
	snapshotReady bool
	snapshotTTL   time.Duration
	now           func() time.Time
	parseFile     func(path, fallbackDir string) (*Session, error)
	readDir       func(string) ([]os.DirEntry, error)
	warnings      []string
}

func NewCatalog(root string) *Catalog {
	root = strings.TrimSpace(root)
	if root != "" {
		root = filepath.Clean(root)
	}
	return &Catalog{
		root:        root,
		cache:       make(map[string]catalogEntry),
		snapshotTTL: 250 * time.Millisecond,
		now:         time.Now,
		parseFile:   parseSessionDetailed,
		readDir:     os.ReadDir,
	}
}

func (c *Catalog) Snapshot() []*Session {
	c.scanMu.Lock()
	defer c.scanMu.Unlock()

	c.mu.Lock()
	nowFn := c.now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()
	if c.snapshotReady && c.snapshotTTL > 0 && now.Sub(c.snapshotAt) < c.snapshotTTL {
		out := cloneSessions(c.snapshot)
		c.mu.Unlock()
		return out
	}
	root := c.root
	cache := cloneCache(c.cache)
	previous := cloneSessions(c.snapshot)
	parseFile := c.parseFile
	readDir := c.readDir
	c.mu.Unlock()

	if parseFile == nil {
		parseFile = parseSessionDetailed
	}
	if readDir == nil {
		readDir = os.ReadDir
	}
	if root == "" {
		return c.finishSnapshot(nil, make(map[string]catalogEntry), nil, nowFn())
	}

	projectDirs, err := readDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			if info, statErr := os.Stat(root); statErr == nil && !info.IsDir() {
				return c.finishSnapshot(previous, cache, []string{catalogWarning(root, err)}, nowFn())
			}
			if len(previous) > 0 {
				return c.finishSnapshot(previous, cache, []string{catalogWarning(root, err)}, nowFn())
			}
			return c.finishSnapshot(nil, make(map[string]catalogEntry), nil, nowFn())
		}
		return c.finishSnapshot(previous, cache, []string{catalogWarning(root, err)}, nowFn())
	}

	currentPaths := make(map[string]struct{})
	out := make([]*Session, 0)
	warnings := make([]string, 0)
	for _, projectDir := range projectDirs {
		if !projectDir.IsDir() {
			continue
		}
		projectPath := filepath.Join(root, projectDir.Name())
		files, err := readDir(projectPath)
		if err != nil {
			if os.IsNotExist(err) {
				if hasCachedProject(cache, projectPath) {
					warnings = append(warnings, catalogWarning(projectPath, err))
					out = appendCachedProject(out, cache, currentPaths, projectPath)
				}
			} else {
				warnings = append(warnings, catalogWarning(projectPath, err))
				out = appendCachedProject(out, cache, currentPaths, projectPath)
			}
			continue
		}
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".jsonl") {
				continue
			}
			id := strings.TrimSuffix(file.Name(), ".jsonl")
			if ValidateID(id) != nil {
				continue
			}
			path := filepath.Join(projectPath, file.Name())
			currentPaths[path] = struct{}{}
			cached, hasCached := cache[path]
			info, err := file.Info()
			if err != nil {
				warnings = append(warnings, catalogWarning(path, err))
				if hasCached && cached.session != nil {
					out = append(out, cloneSession(cached.session))
				}
				continue
			}
			if info.Size() == 0 {
				delete(cache, path)
				continue
			}

			fingerprint := fileFingerprint{size: info.Size(), modTime: info.ModTime().UnixNano()}
			if !hasCached || cached.session == nil || cached.fingerprint != fingerprint {
				parsed, parseErr := parseFile(path, decodeDir(projectDir.Name()))
				if parseErr != nil {
					warnings = append(warnings, catalogWarning(path, parseErr))
				}
				if parsed == nil {
					if parseErr == nil {
						warnings = append(warnings, catalogWarning(path, os.ErrInvalid))
					}
					if !hasCached || cached.session == nil {
						delete(cache, path)
					} else {
						out = append(out, cloneSession(cached.session))
					}
					continue
				}
				cached = catalogEntry{fingerprint: fingerprint, session: parsed}
				cache[path] = cached
			}
			out = append(out, cloneSession(cached.session))
		}
	}

	for path := range cache {
		if _, ok := currentPaths[path]; !ok {
			delete(cache, path)
		}
	}
	return c.finishSnapshot(out, cache, warnings, nowFn())
}

func catalogWarning(path string, err error) string {
	return path + ": " + err.Error()
}

func appendCachedProject(out []*Session, cache map[string]catalogEntry, currentPaths map[string]struct{}, projectPath string) []*Session {
	paths := make([]string, 0)
	for path, entry := range cache {
		if filepath.Dir(path) == projectPath && entry.session != nil {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		currentPaths[path] = struct{}{}
		out = append(out, cloneSession(cache[path].session))
	}
	return out
}

func hasCachedProject(cache map[string]catalogEntry, projectPath string) bool {
	for path, entry := range cache {
		if filepath.Dir(path) == projectPath && entry.session != nil {
			return true
		}
	}
	return false
}

func cloneCache(source map[string]catalogEntry) map[string]catalogEntry {
	if source == nil {
		return make(map[string]catalogEntry)
	}
	out := make(map[string]catalogEntry, len(source))
	for path, entry := range source {
		entry.session = cloneSession(entry.session)
		out[path] = entry
	}
	return out
}

func (c *Catalog) finishSnapshot(snapshot []*Session, cache map[string]catalogEntry, warnings []string, completedAt time.Time) []*Session {
	c.mu.Lock()
	c.cache = cache
	c.snapshot = cloneSessions(snapshot)
	c.snapshotAt = completedAt
	c.snapshotReady = true
	c.warnings = append([]string(nil), warnings...)
	out := cloneSessions(c.snapshot)
	c.mu.Unlock()
	return out
}

// TakeWarnings returns diagnostics produced by the most recent scan and clears
// them, so visible and hidden list calls do not log the same warning twice.
func (c *Catalog) TakeWarnings() []string {
	c.scanMu.Lock()
	defer c.scanMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	warnings := append([]string(nil), c.warnings...)
	c.warnings = nil
	return warnings
}

func cloneSession(item *Session) *Session {
	if item == nil {
		return nil
	}
	copy := *item
	return &copy
}

func cloneSessions(list []*Session) []*Session {
	if list == nil {
		return nil
	}
	out := make([]*Session, 0, len(list))
	for _, item := range list {
		if copy := cloneSession(item); copy != nil {
			out = append(out, copy)
		}
	}
	return out
}
