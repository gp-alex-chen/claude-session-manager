package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"

	"github.com/gp-alex-chen/claude-session-manager/internal/session"
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
	if got, err := a.claudeCmd(""); err != nil || got != "cmd /c claude" {
		t.Fatalf("default command = %q", got)
	}
	if got, err := a.claudeCmd("abc"); err != nil || got != "cmd /c claude -r abc" {
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
	if got, err := a.claudeCmd("abc"); err != nil || got != `pwsh -NoLogo -NoExit -Command "claude -r abc"` {
		t.Fatalf("pwsh resume command = %q", got)
	}
}

func TestClaudeCommandFallsBackWhenPwshMissing(t *testing.T) {
	a, store, logs, _ := testApp(t)
	if err := store.SetShell("pwsh"); err != nil {
		t.Fatal(err)
	}
	a.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	if got, err := a.claudeCmd("abc"); err != nil || got != "cmd /c claude -r abc" {
		t.Fatalf("fallback command = %q", got)
	}
	if !containsLog(*logs, "回退 cmd") {
		t.Fatalf("fallback diagnostic missing: %v", *logs)
	}
}

func TestStartSessionRejectsUnsafeIDsBeforeLaunching(t *testing.T) {
	a, _, _, _ := testApp(t)
	started := []string{}
	a.startPTYFn = func(_, cmdLine, _ string) error {
		started = append(started, cmdLine)
		return nil
	}

	unsafeIDs := []string{
		"abc&whoami", "abc|whoami", "abc>file", "abc<file", "abc^whoami",
		`abc"whoami`, "abc'whoami", "abc%PATH%", "abc!PATH!", "abc\nwhoami", "abc whoami",
		"", "-abc", ".abc", "_abc", strings.Repeat("a", 257),
	}
	for _, id := range unsafeIDs {
		started = nil
		if _, err := a.StartSession(id, `C:\work`); err == nil {
			t.Fatalf("StartSession(%q) unexpectedly succeeded", id)
		}
		if len(started) != 0 {
			t.Fatalf("StartSession(%q) launched command %q", id, started[0])
		}
	}

	if _, err := a.StartSession("a1b2c3-session", `C:\work`); err != nil {
		t.Fatalf("valid session ID was rejected: %v", err)
	}
	if len(started) != 1 || started[0] != "cmd /c claude -r a1b2c3-session" {
		t.Fatalf("valid session command = %v", started)
	}
}

func TestStartSessionValidatesIDsBeforePowerShellCommandConstruction(t *testing.T) {
	a, _, _, _ := testApp(t)
	a.lookPath = func(string) (string, error) { return `C:\Program Files\PowerShell\pwsh.exe`, nil }
	if err := a.SetShell("pwsh"); err != nil {
		t.Fatal(err)
	}
	started := []string{}
	a.startPTYFn = func(_, cmdLine, _ string) error {
		started = append(started, cmdLine)
		return nil
	}

	if _, err := a.StartSession("safe-id", `C:\work`); err != nil {
		t.Fatalf("valid PowerShell session ID was rejected: %v", err)
	}
	if len(started) != 1 || started[0] != `pwsh -NoLogo -NoExit -Command "claude -r safe-id"` {
		t.Fatalf("PowerShell session command = %v", started)
	}
	started = nil
	if _, err := a.StartSession("safe-id&whoami", `C:\work`); err == nil {
		t.Fatal("unsafe PowerShell session ID was accepted")
	}
	if len(started) != 0 {
		t.Fatalf("unsafe PowerShell session launched command %q", started[0])
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

func TestShutdownPreservesAdoptionAlreadyAtAppBoundary(t *testing.T) {
	a, store, _, _ := testApp(t)
	a.terms = terminal.NewManagerWithStart(
		terminal.Callbacks{},
		func(ids []string) error { return store.SaveOpen(ids) },
		func(string, string, int, int, []string) (terminal.Pty, error) {
			return newShutdownPty(), nil
		},
	)
	if err := a.terms.Start("new-runtime", "cmd", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := a.registerPendingAdoption("new-runtime", "real-session"); err != nil {
		t.Fatal(err)
	}

	a.shutdown(context.Background())
	got, err := store.LoadOpen()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"real-session"}) {
		t.Fatalf("open sessions after pending adoption shutdown = %v", got)
	}
}

func TestShutdownExcludesCancelledPendingAdoptionBeforeManagerEntry(t *testing.T) {
	a, store, _, _ := testApp(t)
	a.terms = terminal.NewManagerWithStart(
		terminal.Callbacks{},
		func(ids []string) error { return store.SaveOpen(ids) },
		func(string, string, int, int, []string) (terminal.Pty, error) {
			return newShutdownPty(), nil
		},
	)
	if err := a.terms.Start("new-runtime", "cmd", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := a.registerPendingAdoption("new-runtime", "real-session"); err != nil {
		t.Fatal(err)
	}
	a.cancelPendingAdoption("new-runtime")

	a.shutdown(context.Background())
	got, err := store.LoadOpen()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("open sessions after cancelled pending shutdown = %v, want empty", got)
	}
}

func TestTermKillCancelsInFlightAdoptionBeforeShutdownSnapshot(t *testing.T) {
	a, store, _, _ := testApp(t)
	adoptionPersistStarted := make(chan struct{})
	releaseAdoptionPersist := make(chan struct{})
	var blockOnce sync.Once
	a.terms = terminal.NewManagerWithStart(
		terminal.Callbacks{},
		func(ids []string) error {
			if reflect.DeepEqual(ids, []string{"real-session"}) {
				blockOnce.Do(func() { close(adoptionPersistStarted) })
				<-releaseAdoptionPersist
			}
			return store.SaveOpen(ids)
		},
		func(string, string, int, int, []string) (terminal.Pty, error) {
			return newShutdownPty(), nil
		},
	)
	if err := a.terms.Start("new-runtime", "cmd", "."); err != nil {
		t.Fatal(err)
	}
	adoptDone := make(chan error, 1)
	go func() { adoptDone <- a.AdoptSession("new-runtime", "real-session") }()
	<-adoptionPersistStarted

	killDone := make(chan struct{})
	go func() {
		a.TermKill("new-runtime")
		close(killDone)
	}()
	for {
		a.adoptionMu.Lock()
		pending := a.pendingAdoptions["new-runtime"]
		cancelled := pending != nil && pending.cancelled.Load()
		a.adoptionMu.Unlock()
		if cancelled {
			break
		}
		goruntime.Gosched()
	}

	shutdownDone := make(chan struct{})
	go func() {
		a.shutdown(context.Background())
		close(shutdownDone)
	}()
	close(releaseAdoptionPersist)
	if err := <-adoptDone; err == nil {
		t.Fatal("cancelled adoption unexpectedly succeeded")
	}
	<-killDone
	<-shutdownDone

	got, err := store.LoadOpen()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("open sessions after TermKill + Shutdown = %v, want empty", got)
	}
}

func TestAdoptSessionPersistsRealIDWhileKeepingRuntimeToken(t *testing.T) {
	a, store, _, _ := testApp(t)
	a.terms = terminal.NewManagerWithStart(
		terminal.Callbacks{},
		func(ids []string) error { return store.SaveOpen(ids) },
		func(string, string, int, int, []string) (terminal.Pty, error) {
			return newShutdownPty(), nil
		},
	)
	if err := a.terms.Start("new-runtime", "cmd", "."); err != nil {
		t.Fatal(err)
	}
	if err := a.AdoptSession("new-runtime", "real-session"); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadOpen()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"real-session"}) {
		t.Fatalf("open sessions after adoption = %v, want [real-session]", got)
	}
	a.terms.Kill("new-runtime")
}

func TestAdoptSessionReturnsPersistenceErrorSoFrontendCanRetry(t *testing.T) {
	sentinel := errors.New("persist failed")
	shouldFail := false
	a, _, _, _ := testApp(t)
	a.terms = terminal.NewManagerWithStart(
		terminal.Callbacks{},
		func([]string) error {
			if shouldFail {
				return sentinel
			}
			return nil
		},
		func(string, string, int, int, []string) (terminal.Pty, error) {
			return newShutdownPty(), nil
		},
	)
	if err := a.terms.Start("new-runtime", "cmd", "."); err != nil {
		t.Fatal(err)
	}

	shouldFail = true
	if err := a.AdoptSession("new-runtime", "real-session"); !errors.Is(err, sentinel) {
		t.Fatalf("AdoptSession error = %v, want %v", err, sentinel)
	}
	shouldFail = false
	if err := a.AdoptSession("new-runtime", "real-session"); err != nil {
		t.Fatalf("AdoptSession retry failed: %v", err)
	}
	a.terms.Kill("new-runtime")
}

func TestAdoptSessionRejectsUnsafeIDsBeforePersisting(t *testing.T) {
	a, _, _, _ := testApp(t)
	persistCalls := 0
	a.terms = terminal.NewManagerWithStart(
		terminal.Callbacks{},
		func([]string) error {
			persistCalls++
			return nil
		},
		func(string, string, int, int, []string) (terminal.Pty, error) {
			return newShutdownPty(), nil
		},
	)
	if err := a.terms.Start("new-runtime", "cmd", "."); err != nil {
		t.Fatal(err)
	}
	baselineCalls := persistCalls
	if err := a.AdoptSession("new-runtime", "real&whoami"); err == nil {
		t.Fatal("AdoptSession unexpectedly accepted a Shell metacharacter")
	}
	if persistCalls != baselineCalls {
		t.Fatalf("unsafe adoption reached persistence: calls=%d baseline=%d", persistCalls, baselineCalls)
	}
	a.terms.Kill("new-runtime")
}

func TestAdoptedSessionSurvivesShutdownAndRestoresByRealID(t *testing.T) {
	a, store, _, _ := testApp(t)
	a.terms = terminal.NewManagerWithStart(
		terminal.Callbacks{},
		func(ids []string) error { return store.SaveOpen(ids) },
		func(string, string, int, int, []string) (terminal.Pty, error) {
			return newShutdownPty(), nil
		},
	)
	if err := a.terms.Start("new-runtime", "cmd", `C:\work`); err != nil {
		t.Fatal(err)
	}
	if err := a.AdoptSession("new-runtime", "real-session"); err != nil {
		t.Fatal(err)
	}
	a.shutdown(context.Background())

	restored := NewAppWithStore(store)
	started := []string{}
	restored.startPTYFn = func(token, _, dir string) error {
		started = append(started, token+"|"+dir)
		return nil
	}
	for _, id := range restored.GetOpenSessions() {
		if _, err := restored.StartSession(id, `C:\work`); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(started, []string{`real-session|C:\work`}) {
		t.Fatalf("restored starts = %v, want real session ID", started)
	}
}

func TestAdoptSessionRejectsInvalidBoundary(t *testing.T) {
	a, _, _, _ := testApp(t)
	for _, tt := range []struct {
		name, token, sessionID string
	}{
		{name: "missing token", sessionID: "real-session"},
		{name: "missing session ID", token: "new-runtime"},
		{name: "temporary session ID", token: "new-runtime", sessionID: "new-other"},
		{name: "non-temporary token", token: "real-runtime", sessionID: "real-session"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := a.AdoptSession(tt.token, tt.sessionID); err == nil {
				t.Fatal("AdoptSession unexpectedly accepted invalid input")
			}
		})
	}
}

func TestSessionListsUseInjectedCatalogForVisibleAndHiddenSessions(t *testing.T) {
	a, store, _, root := testApp(t)
	projectsRoot := filepath.Join(root, "claude-projects")
	projectDir := filepath.Join(projectsRoot, "project")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"C:\\work","message":"hello"}` + "\n"
	if err := os.WriteFile(filepath.Join(projectDir, "session-1.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	a.sessionCatalog = session.NewCatalog(projectsRoot)

	visible := a.ListSessions()
	if len(visible) != 1 || visible[0].ID != "session-1" || visible[0].Dir != `C:\work` {
		t.Fatalf("visible sessions = %+v", visible)
	}
	if err := store.SetHidden("session-1", true); err != nil {
		t.Fatal(err)
	}
	hidden := a.ListHiddenSessions()
	if len(hidden) != 1 || hidden[0].ID != "session-1" {
		t.Fatalf("hidden sessions = %+v", hidden)
	}
}

func TestSessionListsExcludeIDsThatCannotBeLaunchedSafely(t *testing.T) {
	a, _, _, root := testApp(t)
	projectsRoot := filepath.Join(root, "claude-projects")
	projectDir := filepath.Join(projectsRoot, "project")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"C:\\work","message":"hello"}` + "\n"
	for _, id := range []string{"safe-session", "unsafe&whoami"} {
		if err := os.WriteFile(filepath.Join(projectDir, id+".jsonl"), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a.sessionCatalog = session.NewCatalog(projectsRoot)

	visible := a.ListSessions()
	if len(visible) != 1 || visible[0].ID != "safe-session" {
		t.Fatalf("visible sessions = %+v, want only safe-session", visible)
	}
}

func TestSessionListLogsCatalogReadWarnings(t *testing.T) {
	a, _, logs, root := testApp(t)
	badRoot := filepath.Join(root, "catalog-root-file")
	if err := os.WriteFile(badRoot, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.sessionCatalog = session.NewCatalog(badRoot)

	_ = a.ListSessions()
	if !containsLog(*logs, "读取会话目录失败") {
		t.Fatalf("catalog warning was not logged: %v", *logs)
	}
}

func TestFrontendBindingMethodsRemainPresent(t *testing.T) {
	typ := reflect.TypeOf(&App{})
	want := []string{
		"CheckForUpdate", "UpdateToLatest", "RenameSession", "DeleteSession", "UnhideSession",
		"GetOpenSessions", "GetShell", "ShellInstalled", "SetShell", "ListSessions", "ListHiddenSessions",
		"StartSession", "StartNew", "AdoptSession", "TermWrite", "TermResize", "TermKill", "NotifyBeep", "DebugLog",
		"GetAgents", "GetVersion", "GetUsageSummary",
		"ListProjects", "ChooseProjectDir", "AddProject", "DeleteProject", "OpenFolder",
		"ListProjectFavorites", "SetProjectFavorite",
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

func TestOpenFolderNormalizesAndPassesExistingDirectory(t *testing.T) {
	a, _, _, root := testApp(t)
	dir := filepath.Join(root, "open-me")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	var opened string
	previous := openFolderFn
	t.Cleanup(func() { openFolderFn = previous })
	openFolderFn = func(path string) error {
		opened = path
		return nil
	}

	if err := a.OpenFolder(filepath.Join(dir, ".")); err != nil {
		t.Fatalf("OpenFolder returned error: %v", err)
	}
	if opened != dir {
		t.Fatalf("opened directory = %q, want %q", opened, dir)
	}
}

func TestOpenFolderRejectsBlankMissingAndFilePaths(t *testing.T) {
	a, _, _, root := testApp(t)
	filePath := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(filePath, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"   ", filepath.Join(root, "missing"), filePath} {
		if err := a.OpenFolder(dir); err == nil {
			t.Fatalf("OpenFolder(%q) unexpectedly succeeded", dir)
		}
	}
}

func TestProjectFavoritesRoundTripThroughApp(t *testing.T) {
	a, _, _, root := testApp(t)
	dir := filepath.Join(root, "favorite-project")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := a.AddProject(dir); err != nil {
		t.Fatal(err)
	}
	if err := a.SetProjectFavorite(filepath.Join(dir, "."), true); err != nil {
		t.Fatal(err)
	}

	favorites := a.ListProjectFavorites()
	if !reflect.DeepEqual(favorites, []string{dir}) {
		t.Fatalf("project favorites = %#v, want %#v", favorites, []string{dir})
	}
	if err := a.SetProjectFavorite(dir, false); err != nil {
		t.Fatal(err)
	}
	if favorites := a.ListProjectFavorites(); len(favorites) != 0 {
		t.Fatalf("project favorites after removal = %#v", favorites)
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
