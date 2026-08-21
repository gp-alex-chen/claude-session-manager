package app

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gp-alex-chen/claude-session-manager/internal/state"
)

func testApp(t *testing.T) (*App, *state.Store, *[]string, string) {
	t.Helper()
	dir := t.TempDir()
	store := state.NewStore(dir)
	logs := []string{}
	a := NewAppWithStore(store)
	a.debugLog = func(msg string) { logs = append(logs, msg) }
	return a, store, &logs, dir
}

func TestSessionMutationsTrimAndUseTransactions(t *testing.T) {
	a, store, _, _ := testApp(t)
	if err := a.RenameSession("abc", "  Friendly name  "); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteSession("abc"); err != nil {
		t.Fatal(err)
	}
	st, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Aliases["abc"] != "Friendly name" || !st.HiddenSet()["abc"] {
		t.Fatalf("state after mutations = %#v", st)
	}
	if err := a.RenameSession("abc", "   "); err != nil {
		t.Fatal(err)
	}
	if err := a.UnhideSession("abc"); err != nil {
		t.Fatal(err)
	}
	st, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Aliases["abc"]; ok || len(st.Hidden) != 0 {
		t.Fatalf("state after clearing = %#v", st)
	}
}

func TestClaudeCommandShellSelection(t *testing.T) {
	a, _, _, _ := testApp(t)
	if got := a.claudeCmd(""); got != "cmd /c claude" {
		t.Fatalf("default command = %q", got)
	}
	if got := a.claudeCmd("-r abc"); got != "cmd /c claude -r abc" {
		t.Fatalf("resume command = %q", got)
	}
	a.lookPath = func(string) (string, error) { return `C:\Program Files\PowerShell\pwsh.exe`, nil }
	if err := a.SetShell("pwsh"); err != nil {
		t.Fatal(err)
	}
	var startedCommand string
	a.startPTYFn = func(_, cmdLine, _ string) error {
		startedCommand = cmdLine
		return nil
	}
	if _, err := a.StartNew(`C:\sessions`); err != nil {
		t.Fatal(err)
	}
	if startedCommand != `pwsh -NoLogo -NoExit -Command "claude "` {
		t.Fatalf("pwsh new command = %q", startedCommand)
	}
	if got := a.claudeCmd("-r abc"); got != `pwsh -NoLogo -NoExit -Command "claude -r abc"` {
		t.Fatalf("pwsh resume command = %q", got)
	}
}

func TestClaudeCommandFallsBackWhenPwshMissing(t *testing.T) {
	a, store, logs, _ := testApp(t)
	if err := store.SetShell("pwsh"); err != nil {
		t.Fatal(err)
	}
	a.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	if got := a.claudeCmd("-r abc"); got != "cmd /c claude -r abc" {
		t.Fatalf("fallback command = %q", got)
	}
	if !containsLog(*logs, "回退 cmd") {
		t.Fatalf("fallback diagnostic missing: %v", *logs)
	}
}

func TestSetShellInvalidNameUsesCmdFallback(t *testing.T) {
	a, store, _, _ := testApp(t)
	if err := a.SetShell("fish"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Shell(); err != nil || got != "cmd" {
		t.Fatalf("stored shell = %q, err=%v", got, err)
	}
}

func TestSetShellPwshMissingReturnsErrorAndPreservesSetting(t *testing.T) {
	a, store, _, _ := testApp(t)
	if err := store.SetShell("pwsh"); err != nil {
		t.Fatal(err)
	}
	a.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	if err := a.SetShell("pwsh"); err == nil {
		t.Fatal("SetShell(pwsh) unexpectedly succeeded")
	}
	if got, err := store.Shell(); err != nil || got != "pwsh" {
		t.Fatalf("stored shell after rejected selection = %q, err=%v", got, err)
	}
}

func TestAppReturnsSaveErrors(t *testing.T) {
	tests := []struct {
		name string
		call func(*App) error
		file string
	}{
		{"rename", func(a *App) error { return a.RenameSession("id", "name") }, "favorites.json"},
		{"delete", func(a *App) error { return a.DeleteSession("id") }, "favorites.json"},
		{"shell", func(a *App) error { return a.SetShell("cmd") }, "settings.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			store := state.NewStore(dir)
			if err := os.Mkdir(filepath.Join(dir, tt.file), 0o755); err != nil {
				t.Fatal(err)
			}
			a := NewAppWithStore(store)
			a.lookPath = func(string) (string, error) { return "", nil }
			if err := tt.call(a); err == nil {
				t.Fatalf("%s unexpectedly succeeded", tt.name)
			}
		})
	}
}

func TestMalformedStateUsesSafeDefaultsAndLogs(t *testing.T) {
	a, _, logs, dir := testApp(t)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "open-sessions.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := a.GetShell(); got != "cmd" {
		t.Fatalf("shell = %q", got)
	}
	if got := a.GetOpenSessions(); got != nil {
		t.Fatalf("open sessions = %v", got)
	}
	if !containsLog(*logs, "settings.json") || !containsLog(*logs, "open-sessions.json") {
		t.Fatalf("diagnostic logs = %v", *logs)
	}
}

func TestFrontendBindingMethodsRemainPresent(t *testing.T) {
	typ := reflect.TypeOf(&App{})
	want := []string{
		"CheckForUpdate", "UpdateToLatest", "RenameSession", "DeleteSession", "UnhideSession",
		"GetOpenSessions", "GetShell", "ShellInstalled", "SetShell", "ListSessions", "ListHiddenSessions",
		"StartSession", "StartNew", "TermWrite", "TermResize", "TermKill", "NotifyBeep", "DebugLog",
		"GetAgents", "GetVersion",
	}
	for _, name := range want {
		if _, ok := typ.MethodByName(name); !ok {
			t.Errorf("missing binding method %s", name)
		}
	}
}

func containsLog(logs []string, want string) bool {
	for _, msg := range logs {
		if strings.Contains(msg, want) {
			return true
		}
	}
	return false
}
