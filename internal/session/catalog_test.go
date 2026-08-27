package session

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCatalogCachesUnchangedFilesAndInvalidatesFingerprints(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	firstPath := filepath.Join(projectDir, "first.jsonl")
	if err := os.WriteFile(firstPath, []byte(`{"type":"user","cwd":"C:\\work","message":"first"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	parseCalls := 0
	catalog := NewCatalog(root)
	now := time.Unix(0, 0)
	catalog.now = func() time.Time { return now }
	catalog.parseFile = func(path, fallbackDir string) (*Session, error) {
		parseCalls++
		return parseSessionDetailed(path, fallbackDir)
	}

	first := catalog.Snapshot()
	first[0].Dir = "mutated outside catalog"
	second := catalog.Snapshot()
	if parseCalls != 1 {
		t.Fatalf("unchanged file parse calls=%d, want 1", parseCalls)
	}
	if !reflectSessionIDs(first, second) {
		t.Fatalf("cached snapshots differ: first=%v second=%v", sessionIDs(first), sessionIDs(second))
	}
	if second[0].Dir == "mutated outside catalog" {
		t.Fatal("cached snapshot leaked a mutable session value")
	}

	now = now.Add(time.Second)
	secondPath := filepath.Join(projectDir, "second.jsonl")
	if err := os.WriteFile(secondPath, []byte(`{"type":"user","cwd":"C:\\work","message":"second"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := sessionIDs(catalog.Snapshot()); !reflectStringSlices(got, []string{"first", "second"}) {
		t.Fatalf("snapshot after add=%v", got)
	}
	if parseCalls != 2 {
		t.Fatalf("new file parse calls=%d, want 2", parseCalls)
	}

	now = now.Add(time.Second)
	if err := os.WriteFile(firstPath, []byte(`{"type":"user","cwd":"C:\\work","message":"first file changed"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := sessionIDs(catalog.Snapshot()); !reflectStringSlices(got, []string{"first", "second"}) {
		t.Fatalf("snapshot after change=%v", got)
	}
	if parseCalls != 3 {
		t.Fatalf("changed file parse calls=%d, want 3", parseCalls)
	}

	now = now.Add(time.Second)
	if err := os.Remove(firstPath); err != nil {
		t.Fatal(err)
	}
	if got := sessionIDs(catalog.Snapshot()); !reflectStringSlices(got, []string{"second"}) {
		t.Fatalf("snapshot after delete=%v", got)
	}
	if parseCalls != 3 {
		t.Fatalf("deleted file caused parse calls=%d, want 3", parseCalls)
	}
}

func TestCatalogTTLStartsAfterScanCompletes(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, "session.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"user","message":"hello"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Unix(0, 0)
	parseCalls := 0
	catalog := NewCatalog(root)
	catalog.now = func() time.Time { return now }
	catalog.parseFile = func(path, fallbackDir string) (*Session, error) {
		parseCalls++
		if parseCalls == 1 {
			now = now.Add(time.Second)
		}
		return parseSessionDetailed(path, fallbackDir)
	}

	catalog.Snapshot()
	now = now.Add(100 * time.Millisecond)
	catalog.Snapshot()
	if parseCalls != 1 {
		t.Fatalf("scan completion TTL caused parse calls=%d, want 1", parseCalls)
	}
}

func TestCatalogPreservesSnapshotsAndReportsReadErrors(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, "session.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"user","message":"hello"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	catalog := NewCatalog(root)
	now := time.Unix(0, 0)
	catalog.now = func() time.Time { return now }
	if got := sessionIDs(catalog.Snapshot()); !reflectStringSlices(got, []string{"session"}) {
		t.Fatalf("initial snapshot=%v", got)
	}
	realReadDir := os.ReadDir
	catalog.readDir = func(path string) ([]os.DirEntry, error) {
		if filepath.Clean(path) == filepath.Clean(root) {
			return nil, os.ErrPermission
		}
		return realReadDir(path)
	}
	now = now.Add(time.Second)
	if got := sessionIDs(catalog.Snapshot()); !reflectStringSlices(got, []string{"session"}) {
		t.Fatalf("root-error snapshot=%v, want old snapshot", got)
	}
	if warnings := catalog.TakeWarnings(); len(warnings) != 1 {
		t.Fatalf("root-error warnings=%v, want one warning", warnings)
	}

	catalog.readDir = func(path string) ([]os.DirEntry, error) {
		if filepath.Clean(path) == filepath.Clean(projectDir) {
			return nil, os.ErrPermission
		}
		return realReadDir(path)
	}
	now = now.Add(time.Second)
	if got := sessionIDs(catalog.Snapshot()); !reflectStringSlices(got, []string{"session"}) {
		t.Fatalf("project-error snapshot=%v, want old snapshot", got)
	}
	if warnings := catalog.TakeWarnings(); len(warnings) != 1 {
		t.Fatalf("project-error warnings=%v, want one warning", warnings)
	}
}

func TestCatalogSerializesConcurrentScansAndReusesTheCompletedSnapshot(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, "session.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"user","message":"hello"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var parseCalls atomic.Int32
	parseStarted := make(chan struct{})
	releaseParse := make(chan struct{})
	catalog := NewCatalog(root)
	catalog.parseFile = func(path, fallbackDir string) (*Session, error) {
		parseCalls.Add(1)
		close(parseStarted)
		<-releaseParse
		return parseSessionDetailed(path, fallbackDir)
	}
	firstDone := make(chan []*Session, 1)
	secondDone := make(chan []*Session, 1)
	go func() { firstDone <- catalog.Snapshot() }()
	<-parseStarted
	go func() { secondDone <- catalog.Snapshot() }()
	close(releaseParse)
	<-firstDone
	<-secondDone
	if got := parseCalls.Load(); got != 1 {
		t.Fatalf("concurrent scan parse calls=%d, want 1", got)
	}
}

func TestCatalogRootDisappearancePreservesReadySnapshotAndReportsWarning(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, "session.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"user","message":"hello"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := NewCatalog(root)
	now := time.Unix(0, 0)
	catalog.now = func() time.Time { return now }
	if got := sessionIDs(catalog.Snapshot()); !reflectStringSlices(got, []string{"session"}) {
		t.Fatalf("initial snapshot=%v", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(projectDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if got := sessionIDs(catalog.Snapshot()); !reflectStringSlices(got, []string{"session"}) {
		t.Fatalf("snapshot after root disappearance=%v", got)
	}
	warnings := catalog.TakeWarnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], root) {
		t.Fatalf("root disappearance warnings=%v", warnings)
	}
}

func TestCatalogProjectDeletionAndRecreationHaveExplicitSemantics(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, "session.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"user","message":"old"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := NewCatalog(root)
	now := time.Unix(0, 0)
	catalog.now = func() time.Time { return now }
	catalog.Snapshot()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(projectDir); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if got := catalog.Snapshot(); len(got) != 0 {
		t.Fatalf("snapshot after project deletion=%v, want empty", sessionIDs(got))
	}
	if warnings := catalog.TakeWarnings(); len(warnings) != 0 {
		t.Fatalf("project deletion warnings=%v, want none", warnings)
	}
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"user","message":"new"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if got := sessionIDs(catalog.Snapshot()); !reflectStringSlices(got, []string{"session"}) {
		t.Fatalf("snapshot after project recreation=%v", got)
	}
}

func TestCatalogReportsParserErrorsAndKeepsUsableResult(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, "session.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"user","message":"usable"}`+"\nnot-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := NewCatalog(root)
	now := time.Unix(0, 0)
	catalog.now = func() time.Time { return now }
	if got := sessionIDs(catalog.Snapshot()); !reflectStringSlices(got, []string{"session"}) {
		t.Fatalf("snapshot with malformed line=%v", got)
	}
	warnings := catalog.TakeWarnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "invalid JSONL") {
		t.Fatalf("parser warnings=%v", warnings)
	}

	if err := os.WriteFile(path, []byte(strings.Repeat("x", 33<<20)), 0o600); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if got := sessionIDs(catalog.Snapshot()); !reflectStringSlices(got, []string{"session"}) {
		t.Fatalf("snapshot with oversized line=%v", got)
	}
	warnings = catalog.TakeWarnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "scan JSONL") {
		t.Fatalf("oversized-line warnings=%v", warnings)
	}

	catalog.parseFile = func(string, string) (*Session, error) {
		return nil, errors.New("parser unavailable")
	}
	now = now.Add(time.Second)
	if got := sessionIDs(catalog.Snapshot()); !reflectStringSlices(got, []string{"session"}) {
		t.Fatalf("snapshot after parser failure=%v", got)
	}
	if warnings = catalog.TakeWarnings(); len(warnings) != 1 || !strings.Contains(warnings[0], "parser unavailable") {
		t.Fatalf("injected parser warnings=%v", warnings)
	}
}

func sessionIDs(list []*Session) []string {
	ids := make([]string, 0, len(list))
	for _, item := range list {
		ids = append(ids, item.ID)
	}
	sort.Strings(ids)
	return ids
}

func reflectSessionIDs(left, right []*Session) bool {
	return reflectStringSlices(sessionIDs(left), sessionIDs(right))
}

func reflectStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
