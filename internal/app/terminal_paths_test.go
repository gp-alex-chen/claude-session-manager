package app

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenTerminalPath(t *testing.T) {
	project := t.TempDir()
	parent := filepath.Join(project, "outputs", "portable")
	file := filepath.Join(parent, "试用 build.exe")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	fileURL := (&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(file)}).String()

	for _, tc := range []struct{ name, dir, target, action, want string }{
		{"relative file", project, "outputs/portable/试用 build.exe", "open", "file:" + file},
		{"file URI", "", fileURL, "open", "file:" + file},
		{"absolute file parent", "", file, "folder", "dir:" + parent},
		{"direct directory", project, `outputs\portable`, "open", "dir:" + parent},
		{"open folder on directory", project, "outputs/portable", "folder", "dir:" + parent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			open := func(kind string) func(string) error {
				return func(path string) error { calls = append(calls, kind+path); return nil }
			}
			if err := openTerminalPath(tc.dir, tc.target, tc.action, open("file:"), open("dir:")); err != nil {
				t.Fatal(err)
			}
			if len(calls) != 1 || calls[0] != tc.want {
				t.Fatalf("opened %v, want %q", calls, tc.want)
			}
		})
	}

	for _, tc := range []struct{ dir, target, action string }{
		{"", "outputs/portable", "open"},
		{`\\server\share`, "outputs/portable", "open"},
		{project, `\\server\share\file.exe`, "open"},
		{project, `\\.\NUL`, "open"},
		{project, "file://server/share/test.exe", "open"},
		{project, "file:///outputs/portable", "open"},
		{project, `C:build.exe`, "open"},
		{project, `\outputs\portable`, "open"},
		{project, "outputs/portable/CON.txt", "open"},
		{project, file + ":stream", "open"},
		{project, "https://example.com/file.exe", "open"},
		{project, "outputs/missing.exe", "folder"},
		{project, file, "other"},
	} {
		t.Run(fmt.Sprintf("deny %q (%s)", tc.target, tc.action), func(t *testing.T) {
			unexpected := func(path string) error { t.Fatalf("unexpected launch %q", path); return nil }
			if err := openTerminalPath(tc.dir, tc.target, tc.action, unexpected, unexpected); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
