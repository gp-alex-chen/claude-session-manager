package usage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestScannerAggregatesProjectRecursivelyAndIsolatesProjects(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "project-a", "session-1", assistantLine("a-1", "", usageJSON(10, 1, 0, 0, 0, 0, 0)))
	writeSession(t, root, "project-a", "session-2", assistantLine("a-2", "", usageJSON(20, 2, 0, 0, 0, 0, 0)))
	writeSession(t, root, "project-a", filepath.Join("subagent", "child"), assistantLine("a-child", "", usageJSON(30, 3, 0, 0, 0, 0, 0)))
	writeSession(t, root, "project-b", "other", assistantLine("b-1", "", usageJSON(100, 4, 0, 0, 0, 0, 0)))

	result := NewScanner(root).Scan("session-1", "project-a")
	if !result.ProjectFound || !result.SessionFound {
		t.Fatalf("found = project:%v session:%v, want both", result.ProjectFound, result.SessionFound)
	}
	if result.SessionTotal.InputTokens != 10 || result.SessionRequestCount != 1 {
		t.Fatalf("session = %+v, want input=10 requests=1", result.SessionTotal)
	}
	if result.ProjectTotal.InputTokens != 60 || result.ProjectTotal.OutputTokens != 6 || result.ProjectRequestCount != 3 {
		t.Fatalf("project = %+v, want recursive project-a total", result.ProjectTotal)
	}

	isolated := NewScanner(root).Scan("session-1", "project-a")
	if isolated.ProjectTotal.InputTokens == 160 || isolated.ProjectTotal.InputTokens != 60 {
		t.Fatalf("project isolation failed: %+v", isolated.ProjectTotal)
	}
}

func TestScannerDeduplicatesMessageIDsAcrossProjectFiles(t *testing.T) {
	root := t.TempDir()
	line := assistantLine("shared-request", "", usageJSON(12, 3, 0, 0, 0, 0, 0))
	writeSession(t, root, "project-a", "session-1", line)
	writeSession(t, root, "project-a", filepath.Join("subagent", "child"), line)

	result := NewScanner(root).Scan("session-1", "project-a")
	if result.ProjectTotal.InputTokens != 12 || result.ProjectTotal.OutputTokens != 3 || result.ProjectRequestCount != 1 {
		t.Fatalf("project = %+v requests=%d, want one cross-file request", result.ProjectTotal, result.ProjectRequestCount)
	}
}

func TestScannerLocatesRealSessionAndReportsUnknownSession(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "project-a", "session-1", assistantLine("a-1", "", usageJSON(5, 6, 0, 0, 0, 0, 0)))

	found := NewScanner(root).Scan("session-1", "")
	if !found.ProjectFound || !found.SessionFound || found.ProjectTotal.InputTokens != 5 {
		t.Fatalf("found = %+v, want session selected by ID", found)
	}

	unknown := NewScanner(root).Scan("missing", "project-a")
	if !unknown.ProjectFound || unknown.SessionFound {
		t.Fatalf("unknown = %+v, want project found and session unavailable", unknown)
	}
	if unknown.ProjectTotal.InputTokens != 5 || unknown.ProjectRequestCount != 1 {
		t.Fatalf("unknown project = %+v, want project data preserved", unknown.ProjectTotal)
	}
}

func TestScannerAcceptsAbsoluteProjectPathWithoutBasenameCollision(t *testing.T) {
	root := t.TempDir()
	// Claude encodes a Windows project path by replacing its drive/path
	// separators with hyphens, e.g. D:\plug\fyne-sidebar becomes
	// D--plug-fyne-sidebar. The other project has the same leaf directory but
	// must not be selected when the absolute path is supplied.
	absoluteProject := `D:\plug\fyne-sidebar`
	encodedProject := encodeClaudeProjectPath(absoluteProject)
	otherProject := encodeClaudeProjectPath(`D:\other\fyne-sidebar`)
	writeSession(t, root, otherProject, "same-session", assistantLine("other", "", usageJSON(22, 0, 0, 0, 0, 0, 0)))
	writeSession(t, root, encodedProject, "same-session", assistantLine("target", "", usageJSON(11, 0, 0, 0, 0, 0, 0)))

	scanner := NewScanner(root)
	absolute := scanner.Scan("same-session", absoluteProject)
	if absolute.ProjectTotal.InputTokens != 11 || absolute.SessionTotal.InputTokens != 11 {
		t.Fatalf("absolute project selection = %+v, want encoded target project", absolute)
	}
	encoded := scanner.Scan("same-session", encodedProject)
	if encoded.ProjectTotal.InputTokens != 11 || encoded.SessionTotal.InputTokens != 11 {
		t.Fatalf("encoded project selection = %+v, want encoded target project", encoded)
	}
}

func TestScannerCachesByPathSizeAndModTime(t *testing.T) {
	root := t.TempDir()
	path := writeSession(t, root, "project-a", "session-1", assistantLine("a-1", "", usageJSON(1, 2, 0, 0, 0, 0, 0)))
	var reads atomic.Int32
	scanner := NewScanner(root)
	scanner.readFile = func(name string) ([]byte, error) {
		reads.Add(1)
		return os.ReadFile(name)
	}

	scanner.Scan("session-1", "project-a")
	scanner.Scan("session-1", "project-a")
	if reads.Load() != 1 {
		t.Fatalf("reads after cache hit = %d, want 1", reads.Load())
	}

	if err := os.WriteFile(path, []byte(assistantLine("a-1", "", usageJSON(9, 2, 0, 0, 0, 0, 0))), 0o600); err != nil {
		t.Fatal(err)
	}
	oldInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, oldInfo.ModTime().Add(time.Second), oldInfo.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	updated := scanner.Scan("session-1", "project-a")
	if reads.Load() != 2 || updated.SessionTotal.InputTokens != 9 {
		t.Fatalf("same-size rewrite did not invalidate cache: reads=%d total=%+v", reads.Load(), updated.SessionTotal)
	}
}

func TestScannerInvalidatesAppendTruncateAndDelete(t *testing.T) {
	root := t.TempDir()
	path := writeSession(t, root, "project-a", "session-1", assistantLine("a-1", "", usageJSON(1, 1, 0, 0, 0, 0, 0)))
	scanner := NewScanner(root)

	initial := scanner.Scan("session-1", "project-a")
	if initial.ProjectTotal.InputTokens != 1 || initial.ProjectRequestCount != 1 {
		t.Fatalf("initial = %+v", initial)
	}
	if err := os.WriteFile(path, []byte(string(mustRead(t, path))+"\n"+assistantLine("a-2", "", usageJSON(2, 2, 0, 0, 0, 0, 0))), 0o600); err != nil {
		t.Fatal(err)
	}
	appended := scanner.Scan("session-1", "project-a")
	if appended.ProjectTotal.InputTokens != 3 || appended.ProjectRequestCount != 2 {
		t.Fatalf("append = %+v, want total input=3 requests=2", appended)
	}
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	truncated := scanner.Scan("session-1", "project-a")
	if truncated.SessionTotal != (Usage{}) || truncated.ProjectTotal != (Usage{}) || truncated.ProjectRequestCount != 0 {
		t.Fatalf("truncate = %+v, want empty reports", truncated)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	deleted := scanner.Scan("session-1", "project-a")
	if deleted.SessionFound || deleted.ProjectRequestCount != 0 {
		t.Fatalf("delete = %+v, want missing session and empty project", deleted)
	}
}

func TestScannerDropsCacheWhenProjectIsDeleted(t *testing.T) {
	root := t.TempDir()
	content := assistantLine("a-1", "", usageJSON(4, 0, 0, 0, 0, 0, 0))
	writeSession(t, root, "project-a", "session-1", content)
	var reads atomic.Int32
	scanner := NewScanner(root)
	scanner.readFile = func(path string) ([]byte, error) {
		reads.Add(1)
		return os.ReadFile(path)
	}
	scanner.Scan("session-1", "project-a")
	if err := os.RemoveAll(filepath.Join(root, "project-a")); err != nil {
		t.Fatal(err)
	}
	missing := scanner.Scan("session-1", "project-a")
	if missing.ProjectFound || missing.SessionFound {
		t.Fatalf("missing = %+v, want project/session unavailable", missing)
	}
	writeSession(t, root, "project-a", "session-1", content)
	recreated := scanner.Scan("session-1", "project-a")
	if !recreated.SessionFound || recreated.SessionTotal.InputTokens != 4 || reads.Load() != 2 {
		t.Fatalf("recreated = %+v, reads=%d, want cache miss after deletion", recreated, reads.Load())
	}
}

func TestScannerDoesNotCacheUnreadableFile(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "project-a", "session-1", assistantLine("a-1", "", usageJSON(7, 0, 0, 0, 0, 0, 0)))
	var fail atomic.Bool
	scanner := NewScanner(root)
	scanner.readFile = func(path string) ([]byte, error) {
		if fail.Load() {
			return nil, errors.New("temporary read failure")
		}
		return os.ReadFile(path)
	}
	fail.Store(true)
	failed := scanner.Scan("session-1", "project-a")
	if failed.SessionFound || len(failed.Warnings) == 0 {
		t.Fatalf("failed = %+v, want warning and no session", failed)
	}
	fail.Store(false)
	recovered := scanner.Scan("session-1", "project-a")
	if !recovered.SessionFound || recovered.SessionTotal.InputTokens != 7 {
		t.Fatalf("recovered = %+v, want fresh read after failure", recovered)
	}
}

func TestScannerConcurrentQueriesAreRaceSafe(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "project-a", "session-1", assistantLine("a-1", "", usageJSON(7, 8, 0, 0, 0, 0, 0)))
	scanner := NewScanner(root)
	done := make(chan struct{}, 32)
	for i := 0; i < 32; i++ {
		go func() {
			result := scanner.Scan("session-1", "project-a")
			if !result.SessionFound || result.ProjectTotal.InputTokens != 7 {
				t.Errorf("concurrent result = %+v", result)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 32; i++ {
		<-done
	}
}

func writeSession(t *testing.T, root, project, session, content string) string {
	t.Helper()
	path := filepath.Join(root, project, session+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func encodeClaudeProjectPath(path string) string {
	return strings.NewReplacer(":", "-", "\\", "-", "/", "-").Replace(path)
}
