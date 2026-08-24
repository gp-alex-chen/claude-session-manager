package usage

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Summary contains usage for the requested session and its containing
// project. SessionFound and ProjectFound distinguish missing data from a real
// zero-valued report.
type Summary struct {
	Latest              *Usage   `json:"latest,omitempty"`
	SessionTotal        Usage    `json:"session_total"`
	SessionRequestCount int      `json:"session_request_count"`
	ProjectTotal        Usage    `json:"project_total"`
	ProjectRequestCount int      `json:"project_request_count"`
	SessionFound        bool     `json:"session_found"`
	ProjectFound        bool     `json:"project_found"`
	Warnings            []string `json:"warnings,omitempty"`
}

type fileFingerprint struct {
	size    int64
	modTime time.Time
}

type cachedFile struct {
	fingerprint fileFingerprint
	report      Report
}

// Scanner finds Claude JSONL sessions below one injected projects root and
// caches parsed files until their size or modification time changes.
type Scanner struct {
	root string

	mu    sync.Mutex
	cache map[string]cachedFile

	readFile func(string) ([]byte, error)
}

// NewScanner creates a scanner rooted at a Claude projects directory. The
// root is injected so callers and tests do not need to depend on a user home
// directory.
func NewScanner(projectsRoot string) *Scanner {
	return &Scanner{
		root:     filepath.Clean(projectsRoot),
		cache:    make(map[string]cachedFile),
		readFile: os.ReadFile,
	}
}

// Scan returns the latest request and deduplicated totals for sessionID and
// for all JSONL files recursively contained by its project directory.
// projectDir may be the direct project folder name under the root, an absolute
// project path, or the encoded Claude project name. If it is empty or cannot
// be resolved, sessionID is used to locate the direct <sessionID>.jsonl file.
// Missing or transiently unreadable files are reported through Warnings and
// do not leave stale entries in the cache.
func (s *Scanner) Scan(sessionID, projectDir string) Summary {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := Summary{}
	projectPath, projectFound := s.resolveProject(projectDir, sessionID)
	result.ProjectFound = projectFound
	if !projectFound {
		s.purgeMissingCache()
		return result
	}

	files, warnings := collectJSONL(projectPath)
	result.Warnings = append(result.Warnings, warnings...)
	active := make(map[string]struct{}, len(files))
	projectReports := make([]projectReport, 0, len(files))
	for _, path := range files {
		active[path] = struct{}{}
		report, ok, warning := s.reportFor(path)
		if warning != "" {
			result.Warnings = append(result.Warnings, warning)
		}
		if !ok {
			continue
		}
		projectReports = append(projectReports, projectReport{path: path, report: report})
	}
	s.purgeDeleted(projectPath, active)

	result.ProjectTotal, result.ProjectRequestCount = aggregateProjectReports(projectReports)

	if sessionID == "" {
		return result
	}
	sessionPath := filepath.Join(projectPath, sessionID+".jsonl")
	if _, err := os.Stat(sessionPath); err != nil {
		if !os.IsNotExist(err) {
			result.Warnings = append(result.Warnings, fmt.Sprintf("session %q unavailable: %v", sessionID, err))
		}
		return result
	}
	report, ok, warning := s.reportFor(sessionPath)
	if warning != "" {
		result.Warnings = append(result.Warnings, warning)
	}
	if !ok {
		return result
	}
	result.SessionFound = true
	if report.Latest != nil {
		latest := *report.Latest
		result.Latest = &latest
	}
	result.SessionTotal = report.Total
	result.SessionRequestCount = report.RequestCount
	return result
}

type projectReport struct {
	path   string
	report Report
}

func aggregateProjectReports(reports []projectReport) (Usage, int) {
	values := make(map[string]Usage)
	for _, item := range reports {
		for key, current := range item.report.entries {
			// A line-number fallback is only unique within its source file. Keep
			// it file-scoped while allowing real IDs to deduplicate project-wide.
			if strings.HasPrefix(key, "line:") {
				key = item.path + "#" + key
			}
			values[key] = current
		}
	}
	var total Usage
	for _, value := range values {
		total = add(total, value)
	}
	return total, len(values)
}

func (s *Scanner) reportFor(path string) (Report, bool, string) {
	info, err := os.Stat(path)
	if err != nil {
		delete(s.cache, path)
		if os.IsNotExist(err) {
			return Report{}, false, ""
		}
		return Report{}, false, fmt.Sprintf("usage file %q unavailable: %v", path, err)
	}
	fingerprint := fileFingerprint{size: info.Size(), modTime: info.ModTime()}
	if cached, ok := s.cache[path]; ok && cached.fingerprint == fingerprint {
		return cached.report, true, ""
	}
	data, err := s.readFile(path)
	if err != nil {
		delete(s.cache, path)
		return Report{}, false, fmt.Sprintf("usage file %q unavailable: %v", path, err)
	}
	report, err := ParseBytes(data)
	if err != nil {
		delete(s.cache, path)
		return Report{}, false, fmt.Sprintf("usage file %q could not be parsed: %v", path, err)
	}
	s.cache[path] = cachedFile{fingerprint: fingerprint, report: report}
	return report, true, ""
}

func (s *Scanner) purgeDeleted(projectPath string, active map[string]struct{}) {
	for path := range s.cache {
		rel, err := filepath.Rel(projectPath, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if _, ok := active[path]; !ok {
			delete(s.cache, path)
		}
	}
}

func (s *Scanner) purgeMissingCache() {
	for path := range s.cache {
		if _, err := os.Stat(path); err != nil {
			delete(s.cache, path)
		}
	}
}

func (s *Scanner) resolveProject(projectDir, sessionID string) (string, bool) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return "", false
	}
	if projectDir != "" {
		for _, entry := range entries {
			if !entry.IsDir() || !matchesProjectDir(entry.Name(), s.root, projectDir) {
				continue
			}
			return filepath.Join(s.root, entry.Name()), true
		}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(s.root, entry.Name(), sessionID+".jsonl")
		if _, err := os.Stat(path); err == nil {
			return filepath.Dir(path), true
		}
	}
	return "", false
}

func matchesProjectDir(entryName, root, projectDir string) bool {
	for _, candidate := range projectDirCandidates(root, projectDir) {
		if strings.EqualFold(entryName, candidate) {
			return true
		}
	}
	return false
}

func projectDirCandidates(root, projectDir string) []string {
	clean := filepath.Clean(projectDir)
	abs := filepath.IsAbs(clean)
	candidates := []string{projectDir}
	if !abs {
		candidates = append(candidates, filepath.Base(clean))
	}
	if abs {
		encoded := strings.NewReplacer(":", "-", "\\", "-", "/", "-").Replace(clean)
		candidates = append(candidates, encoded)
	}
	// A path that is already below the root can be compared by its relative
	// first component without allowing traversal outside the projects root.
	if rel, err := filepath.Rel(root, clean); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) > 0 && parts[0] != "" {
			candidates = append(candidates, parts[0])
		}
	}
	return candidates
}

func collectJSONL(projectPath string) ([]string, []string) {
	var files []string
	var warnings []string
	err := filepath.WalkDir(projectPath, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("usage path %q unavailable: %v", path, err))
			return nil
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".jsonl") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("usage project %q unavailable: %v", projectPath, err))
	}
	sort.Strings(files)
	return files, warnings
}
