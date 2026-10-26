package app

import (
	"errors"
	"github.com/gp-alex-chen/claude-session-manager/internal/provider"
	"os"
	"path/filepath"
	"testing"
)

func TestResolvedCodexLaunchDoesNotRepeatStalePathLookup(t *testing.T) {
	a, _, _, root := testApp(t)
	bin := filepath.Join(root, "official install", "codex.exe")
	if err := os.MkdirAll(filepath.Dir(bin), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("not executed"), 0600); err != nil {
		t.Fatal(err)
	}
	a.lookPath = func(string) (string, error) { return "", errors.New("stale PATH") }
	command, err := a.renderAgentCommand(provider.Codex, provider.LaunchSpec{Command: "codex", Executable: bin, Args: []string{"--remote", "ws://127.0.0.1:12345"}})
	if err != nil || command != "cmd /c codex --remote ws://127.0.0.1:12345" {
		t.Fatalf("verified native executable rejected by stale second lookup: %q %v", command, err)
	}
}
