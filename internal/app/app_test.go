package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/gp-alex-chen/claude-session-manager/internal/state"
	"github.com/gp-alex-chen/claude-session-manager/internal/terminal"
	"github.com/gp-alex-chen/claude-session-manager/internal/usage"
	"github.com/wailsapp/wails/v2/pkg/runtime"
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

func TestShutdownPreservesOpenSessions(t *testing.T) {
	a, store, _, _ := testApp(t)
	a.terms = terminal.NewManagerWithStart(
		terminal.Callbacks{},
		func(ids []string) error { return store.SaveOpen(ids) },
		func(string, string, int, int, []string) (terminal.Pty, error) {
			return newShutdownPty(), nil
		},
	)
	for _, id := range []string{"session-b", "session-a"} {
		if err := a.terms.Start(id, "cmd", "."); err != nil {
			t.Fatal(err)
		}
	}

	a.shutdown(context.Background())
	got, err := store.LoadOpen()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"session-a", "session-b"}) {
		t.Fatalf("open sessions after shutdown = %v", got)
	}
}

func TestFrontendBindingMethodsRemainPresent(t *testing.T) {
	typ := reflect.TypeOf(&App{})
	want := []string{
		"CheckForUpdate", "UpdateToLatest", "RenameSession", "DeleteSession", "UnhideSession",
		"GetOpenSessions", "GetShell", "ShellInstalled", "SetShell", "ListSessions", "ListHiddenSessions",
		"StartSession", "StartNew", "TermWrite", "TermResize", "TermKill", "NotifyBeep", "DebugLog",
		"GetAgents", "GetVersion", "GetUsageSummary",
		"ListProjects", "ChooseProjectDir", "AddProject", "DeleteProject",
	}
	for _, name := range want {
		if _, ok := typ.MethodByName(name); !ok {
			t.Errorf("missing binding method %s", name)
		}
	}
}

func TestProjectDirectoriesNormalizeDuplicatesWithoutSavingTwice(t *testing.T) {
	a, _, _, root := testApp(t)
	firstDir := filepath.Join(root, "project")
	if err := os.MkdirAll(firstDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := a.AddProject(firstDir); err != nil {
		t.Fatal(err)
	}
	if err := a.AddProject(firstDir + string(os.PathSeparator) + "."); err != nil {
		t.Fatalf("duplicate AddProject should be idempotent: %v", err)
	}

	got := a.ListProjects()
	if !reflect.DeepEqual(got, []string{firstDir}) {
		t.Fatalf("projects after duplicate add = %#v, want one normalized directory", got)
	}
}

func TestChooseProjectDirUsesInjectedChooserAndSupportsCancel(t *testing.T) {
	a, _, _, _ := testApp(t)
	called := 0
	a.chooseDirFn = func(ctx context.Context, dialogOptions runtime.OpenDialogOptions) (string, error) {
		called++
		if ctx == nil || dialogOptions.Title == "" {
			t.Fatal("chooser received incomplete context or options")
		}
		return `C:\chosen`, nil
	}
	got, err := a.ChooseProjectDir()
	if err != nil || got != `C:\chosen` || called != 1 {
		t.Fatalf("chosen dir=%q err=%v calls=%d", got, err, called)
	}

	a.chooseDirFn = func(context.Context, runtime.OpenDialogOptions) (string, error) { return "", nil }
	got, err = a.ChooseProjectDir()
	if err != nil || got != "" {
		t.Fatalf("cancel result=%q err=%v", got, err)
	}
	if projects := a.ListProjects(); len(projects) != 0 {
		t.Fatalf("chooser should not save projects: %v", projects)
	}
}

func TestAddProjectRejectsBlankMissingAndFilePaths(t *testing.T) {
	a, _, _, root := testApp(t)
	filePath := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(filePath, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"   ", filepath.Join(root, "missing"), filePath} {
		if err := a.AddProject(dir); err == nil {
			t.Fatalf("AddProject(%q) unexpectedly succeeded", dir)
		}
	}
}

func TestAddProjectAcceptsDirectoryAndRejectsCaseInsensitiveDuplicate(t *testing.T) {
	a, _, _, _ := testApp(t)
	dir := t.TempDir()
	if err := a.AddProject(dir); err != nil {
		t.Fatal(err)
	}
	if err := a.AddProject(strings.ToUpper(dir)); err != nil {
		t.Fatalf("case-insensitive duplicate should be idempotent: %v", err)
	}
	projects := a.ListProjects()
	if len(projects) != 1 || !strings.EqualFold(projects[0], dir) {
		t.Fatalf("projects after duplicate add = %v", projects)
	}
}

func TestListProjectsCorruptionReturnsSafeListAndLogs(t *testing.T) {
	a, _, logs, root := testApp(t)
	if err := os.WriteFile(filepath.Join(root, "projects.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	projects := a.ListProjects()
	if projects == nil || len(projects) != 0 {
		t.Fatalf("projects after corruption = %v", projects)
	}
	if !containsLog(*logs, "projects.json") {
		t.Fatalf("projects corruption diagnostic missing: %v", *logs)
	}
}

func TestDeleteProjectRemovesConfigurationOnly(t *testing.T) {
	a, _, _, _ := testApp(t)
	if err := a.DeleteProject("   "); err == nil {
		t.Fatal("DeleteProject(blank) unexpectedly succeeded")
	}
	dir := t.TempDir()
	if err := a.AddProject(dir); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteProject(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("DeleteProject removed the real directory: %v", err)
	}
	if projects := a.ListProjects(); len(projects) != 0 {
		t.Fatalf("projects after deletion = %v", projects)
	}
}

func TestGetUsageSummaryUsesInjectedProjectsRoot(t *testing.T) {
	a, _, _, _ := testApp(t)
	root := t.TempDir()
	project := filepath.Join(root, "project-a")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","message":{"id":"request-1","usage":{"input_tokens":12,"output_tokens":3,"cache_read_input_tokens":5}}}`
	if err := os.WriteFile(filepath.Join(project, "session-1.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	a.usageScanner = usage.NewScanner(root)

	got := a.GetUsageSummary("session-1", "project-a")
	if !got.ProjectFound || !got.SessionFound {
		t.Fatalf("found = project:%v session:%v, want both", got.ProjectFound, got.SessionFound)
	}
	if got.SessionTotal.InputTokens != 12 || got.SessionTotal.OutputTokens != 3 || got.ProjectTotal.CacheReadInputTokens != 5 {
		t.Fatalf("summary = %+v, want injected project data", got)
	}
}

func TestGetUsageSummaryUnknownRootIsUnavailableWithoutPanic(t *testing.T) {
	a, _, _, _ := testApp(t)
	a.usageScanner = usage.NewScanner(filepath.Join(t.TempDir(), "does-not-exist"))

	got := a.GetUsageSummary("missing", "project-a")
	if got.ProjectFound || got.SessionFound || got.ProjectRequestCount != 0 || got.SessionRequestCount != 0 {
		t.Fatalf("summary = %+v, want unavailable root with no found data", got)
	}
}

func TestDefaultUsageScannerUsesUserHomeProjectsRoot(t *testing.T) {
	previous := userHomeDir
	home := t.TempDir()
	userHomeDir = func() (string, error) { return home, nil }
	defer func() { userHomeDir = previous }()

	project := filepath.Join(home, ".claude", "projects", "project-a")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","message":{"id":"request-1","usage":{"input_tokens":7}}}`
	if err := os.WriteFile(filepath.Join(project, "session-1.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	a := NewAppWithStore(state.NewStore(t.TempDir()))
	got := a.GetUsageSummary("session-1", "project-a")
	if !got.ProjectFound || !got.SessionFound || got.ProjectTotal.InputTokens != 7 {
		t.Fatalf("summary = %+v, want user-home projects data", got)
	}
}

func TestDefaultUsageScannerDoesNotFallbackToCurrentDirectory(t *testing.T) {
	previous := userHomeDir
	userHomeDir = func() (string, error) { return "", errors.New("home unavailable") }
	defer func() { userHomeDir = previous }()

	a := NewAppWithStore(state.NewStore(t.TempDir()))
	got := a.GetUsageSummary("session", "project")
	if got.ProjectFound || got.SessionFound {
		t.Fatalf("summary = %+v, want unavailable scanner", got)
	}
	if len(got.Warnings) != 1 || got.Warnings[0] != "usage scanner unavailable" {
		t.Fatalf("warnings = %v, want explicit unavailable warning", got.Warnings)
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

type shutdownPty struct {
	once sync.Once
	done chan struct{}
}

func newShutdownPty() *shutdownPty {
	return &shutdownPty{done: make(chan struct{})}
}

func (p *shutdownPty) Read([]byte) (int, error) {
	<-p.done
	return 0, io.EOF
}

func (p *shutdownPty) Write(data []byte) (int, error) { return len(data), nil }
func (p *shutdownPty) Resize(int, int) error          { return nil }
func (p *shutdownPty) Close() error {
	p.once.Do(func() { close(p.done) })
	return nil
}
