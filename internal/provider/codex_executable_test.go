package provider

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCodexFindsOfficialStandaloneWhenInheritedPathIsStale(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "Programs", "OpenAI", "Codex", "bin", "codex.exe")
	if err := os.MkdirAll(filepath.Dir(bin), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("synthetic executable - never run"), 0600); err != nil {
		t.Fatal(err)
	}
	lookup := func(string) (string, error) { return "", errors.New("stale process PATH") }
	getenv := func(name string) string {
		if name == "LOCALAPPDATA" {
			return root
		}
		return ""
	}
	got, err := resolveCodexExecutable(lookup, getenv)
	if err != nil || got != bin {
		t.Fatalf("official native install not found: %q %v", got, err)
	}
	custom := filepath.Join(root, "custom install")
	os.MkdirAll(custom, 0700)
	os.WriteFile(filepath.Join(custom, "codex.exe"), []byte("fixture"), 0600)
	got, err = resolveCodexExecutable(lookup, func(name string) string {
		if name == "CODEX_INSTALL_DIR" {
			return custom
		}
		return getenv(name)
	})
	if err != nil || got != filepath.Join(custom, "codex.exe") {
		t.Fatal(got, err)
	}
	preferred := filepath.Join(root, "preferred.exe")
	got, err = resolveCodexExecutable(func(string) (string, error) { return preferred, nil }, getenv)
	if err != nil || got != preferred {
		t.Fatal("fallback overrode explicit PATH choice", got, err)
	}
}

func TestCodexTerminalEnvironmentPrefersResolvedDirectoryAndKeepsInheritedPath(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "official install", "codex.exe")
	inherited := []string{"Path=old-tool-directory", "UNCHANGED=fixture"}
	got := codexExecutableEnvironment(bin, inherited)
	want := []string{"PATH=" + filepath.Dir(bin) + string(os.PathListSeparator) + "old-tool-directory"}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(inherited, []string{"Path=old-tool-directory", "UNCHANGED=fixture"}) {
		t.Fatal(got, inherited)
	}
}

func TestCodexStandaloneFallbackDoesNotSearchCurrentDirectory(t *testing.T) {
	lookup := func(string) (string, error) { return "", errors.New("missing") }
	if _, err := resolveCodexExecutable(lookup, func(string) string { return "relative-directory" }); err == nil {
		t.Fatal("relative fallback accepted")
	}
}
