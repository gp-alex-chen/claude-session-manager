//go:build debuglog

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDebugLogDiagnosticBuildWritesToIsolatedDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	DebugLog("local diagnostic one")
	DebugLog("local diagnostic two")
	data, err := os.ReadFile(filepath.Join(root, "ClaudeSessionManager", "agents-debug.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[0], "local diagnostic one") || !strings.HasSuffix(lines[1], "local diagnostic two") {
		t.Fatalf("unexpected diagnostic log: %q", data)
	}
}
