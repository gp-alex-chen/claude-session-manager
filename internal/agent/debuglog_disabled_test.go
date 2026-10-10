//go:build !debuglog

package agent

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDebugLogDefaultBuildDoesNotWrite(t *testing.T) {
	t.Run("does not create a log directory", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "appdata")
		t.Setenv("LOCALAPPDATA", root)
		DebugLog("default build diagnostic")
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatalf("logging created an application data directory: %v", err)
		}
	})
	t.Run("preserves an existing log", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("LOCALAPPDATA", root)
		dir := filepath.Join(root, "ClaudeSessionManager")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "agents-debug.log")
		before := bytes.Repeat([]byte("existing log\n"), 60_000)
		if err := os.WriteFile(path, before, 0o600); err != nil {
			t.Fatal(err)
		}
		DebugLog("must not append or truncate")
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatal("default build changed an existing log")
		}
	})
}
