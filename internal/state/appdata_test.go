package state

import (
	"path/filepath"
	"testing"
)

func TestDefaultDirUsesFixedLocalAppData(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	want := filepath.Join(root, "ClaudeSessionManager")
	if got := DefaultDir(); got != want {
		t.Fatalf("DefaultDir() = %q, want fixed application directory %q", got, want)
	}
}
