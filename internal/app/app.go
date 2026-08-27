package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gp-alex-chen/claude-session-manager/internal/agent"
	"github.com/gp-alex-chen/claude-session-manager/internal/notify"
	"github.com/gp-alex-chen/claude-session-manager/internal/session"
	"github.com/gp-alex-chen/claude-session-manager/internal/state"
	"github.com/gp-alex-chen/claude-session-manager/internal/terminal"
	"github.com/gp-alex-chen/claude-session-manager/internal/usage"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type SessionInfo struct {
	ID   string `json:"id"`
	Dir  string `json:"dir"`
	Name string `json:"name"`
	Time string `json:"time"`
}

type pendingAdoption struct {
	id    string
	count int
	cancelled atomic.Bool
}

type App struct {
	lifecycleMu  sync.Mutex
	adoptionMu   sync.Mutex
	ctx          context.Context
	terms        *terminal.Manager
	store        *state.Store
	watcher      *agent.Watcher
	lifecycleID  uint64
	lookPath     func(string) (string, error)
	debugLog     func(string)
	startPTYFn   func(string, string, string) error
	chooseDirFn  func(context.Context, runtime.OpenDialogOptions) (string, error)
	usageScanner *usage.Scanner
	sessionCatalog *session.Catalog
	pendingAdoptions map[string]*pendingAdoption
	cancelledAdoptions map[string]struct{}
}

func NewApp() *App {
	return NewAppWithStore(state.Default())
}

func NewAppWithStore(store *state.Store) *App {
	if store == nil {
		store = state.Default()
	}
	a := &App{
		store:        store,
		lookPath:     exec.LookPath,
		debugLog:     agent.DebugLog,
		chooseDirFn:  runtime.OpenDirectoryDialog,
		usageScanner: defaultUsageScanner(),
		sessionCatalog: defaultSessionCatalog(),
		pendingAdoptions: make(map[string]*pendingAdoption),
		cancelledAdoptions: make(map[string]struct{}),
	}
	a.terms = terminal.NewManager(terminal.Callbacks{}, func(ids []string) error {
		return a.store.SaveOpen(ids)
	})
	a.terms.SetPersistErrorHandler(func(err error) { a.log("持久化打开会话失败: " + err.Error()) })
	return a
}

var userHomeDir = os.UserHomeDir

func defaultUsageScanner() *usage.Scanner {
	home, err := userHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}
	return usage.NewScanner(filepath.Join(home, ".claude", "projects"))
}
func defaultSessionCatalog() *session.Catalog {
	home, err := userHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}
	return session.NewCatalog(filepath.Join(home, ".claude", "projects"))
}
func (a *App) startup(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	watcher := agent.NewWatcher(func(list []agent.AgentInfo) {
		runtime.EventsEmit(ctx, "agents:update", list)
	})
	// Register the candidate before stopping the old watcher. Stop may wait on
	// its fetch loop, so it must never run while lifecycleMu is held.
	a.lifecycleMu.Lock()
	old := a.watcher
	a.lifecycleID++
	lifecycleID := a.lifecycleID
	a.ctx = ctx
	a.watcher = watcher
	a.lifecycleMu.Unlock()
	if old != nil {
		old.Stop()
	}
	// Recheck the generation before installing callbacks or starting the
	// candidate. A concurrent Startup/Shutdown may have made it stale.
	a.lifecycleMu.Lock()
	current := a.lifecycleID == lifecycleID && a.watcher == watcher
	if current {
		a.terms.SetCallbacks(terminal.Callbacks{
			Data: func(token, data string) { runtime.EventsEmit(ctx, "term:data", token, data) },
			Exit: func(token string) { runtime.EventsEmit(ctx, "term:exit", token) },
		})
		watcher.Start(ctx)
	}
	a.lifecycleMu.Unlock()
}
func (a *App) shutdown(context.Context) {
	a.lifecycleMu.Lock()
	watcher := a.watcher
	a.lifecycleID++
	a.watcher = nil
	a.ctx = nil
	a.lifecycleMu.Unlock()
	if watcher != nil {
		watcher.Stop()
	}
	a.closeAllTerms()
}
func (a *App) Startup(ctx context.Context)  { a.startup(ctx) }
func (a *App) Shutdown(ctx context.Context) { a.shutdown(ctx) }
func (a *App) ListSessions() []SessionInfo {
	st, err := a.store.Load()
	if err != nil {
		a.DebugLog("读取 favorites.json 失败: " + err.Error())
	}
	hidden, aliases := st.HiddenSet(), st.Aliases
	out := []SessionInfo{}
	for _, s := range a.sessionSnapshot() {
		if hidden[s.ID] {
			continue
		}
		name := session.DisplayName(s)
		if n := aliases[s.ID]; n != "" {
			name = n
		}
		out = append(out, SessionInfo{s.ID, s.Dir, name, s.Time.Format("01-02 15:04")})
	}
	return out
}
func (a *App) ListHiddenSessions() []SessionInfo {
	st, err := a.store.Load()
	if err != nil {
		a.DebugLog("读取 favorites.json 失败: " + err.Error())
	}
	hidden := st.HiddenSet()
	out := []SessionInfo{}
	for _, s := range a.sessionSnapshot() {
		if !hidden[s.ID] {
			continue
		}
		name := session.DisplayName(s)
		if n := st.Aliases[s.ID]; n != "" {
			name = n
		}
		out = append(out, SessionInfo{s.ID, s.Dir, name, s.Time.Format("01-02 15:04")})
	}
	return out
}
func (a *App) sessionSnapshot() []*session.Session {
	if a.sessionCatalog == nil {
		return nil
	}
	snapshot := a.sessionCatalog.Snapshot()
	for _, warning := range a.sessionCatalog.TakeWarnings() {
		a.log("读取会话目录失败: " + warning)
	}
	return snapshot
}
func (a *App) StartSession(id, dir string) (string, error) {
	if err := validateSessionID(id); err != nil {
		return "", err
	}
	if a.terms.IsRunning(id) {
		return id, nil
	}
	cmdLine, err := a.claudeCmd(id)
	if err != nil {
		return "", err
	}
	if err := a.startPTY(id, cmdLine, dir); err != nil {
		return "", err
	}
	return id, nil
}
func (a *App) StartNew(dir string) (string, error) {
	token := "new-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	cmdLine, err := a.claudeCmd("")
	if err != nil {
		return "", err
	}
	if err := a.startPTY(token, cmdLine, dir); err != nil {
		return "", err
	}
	return token, nil
}
func (a *App) startPTY(token, cmdLine, dir string) error {
	if a.startPTYFn != nil {
		err := a.startPTYFn(token, cmdLine, dir)
		if err == nil {
			a.clearAdoptionCancellation(token)
		}
		return err
	}
	err := a.terms.Start(token, cmdLine, dir)
	if err == nil {
		a.clearAdoptionCancellation(token)
	}
	return err
}
func (a *App) TermWrite(token, b64 string) {
	raw, err := terminal.DecodeInput(b64)
	if err == nil {
		a.terms.Write(token, raw)
	}
}
func (a *App) TermResize(token string, cols, rows int) { a.terms.Resize(token, cols, rows) }
func (a *App) TermKill(token string) {
	a.cancelPendingAdoption(token)
	if a.terms != nil {
		a.terms.Kill(token)
	}
}
func (a *App) NotifyBeep()                             { notify.Beep() }
func (a *App) persistOpenSessions() error {
	if a.store == nil || a.terms == nil {
		return nil
	}
	a.adoptionMu.Lock()
	defer a.adoptionMu.Unlock()
	return a.store.SaveOpen(a.terms.OpenIDsWithPending(a.pendingAdoptionIDsLocked()))
}

func (a *App) claudeCmd(sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID != "" {
		if err := validateSessionID(sessionID); err != nil {
			return "", err
		}
	}
	if a.GetShell() == "pwsh" {
		if a.shellAvailable("pwsh") {
			if sessionID == "" {
				return `pwsh -NoLogo -NoExit -Command "claude "`, nil
			}
			return `pwsh -NoLogo -NoExit -Command "claude -r ` + sessionID + `"`, nil
		}
		a.DebugLog("pwsh 当前不可用，会话回退 cmd 启动")
	}
	if sessionID == "" {
		return "cmd /c claude", nil
	}
	return "cmd /c claude -r " + sessionID, nil
}
func (a *App) log(msg string) {
	if a.debugLog != nil {
		a.debugLog(msg)
	}
}
func (a *App) DebugLog(msg string) { a.log(msg) }
func (a *App) runtimeContext() context.Context {
	a.lifecycleMu.Lock()
	ctx := a.ctx
	a.lifecycleMu.Unlock()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
func (a *App) GetAgents() []agent.AgentInfo {
	a.lifecycleMu.Lock()
	watcher := a.watcher
	ctx := a.ctx
	if watcher == nil {
		watcher = agent.NewWatcher(nil)
		a.watcher = watcher
	}
	a.lifecycleMu.Unlock()
	if ctx == nil {
		ctx = a.runtimeContext()
	}
	return watcher.GetContext(ctx)
}
func (a *App) GetVersion() string { return Version }

// GetUsageSummary returns token usage for a session and its project. When the
// user home cannot be resolved, the scanner is unavailable rather than
// falling back to a relative path that could scan the current directory.
func (a *App) GetUsageSummary(sessionID, projectDir string) usage.Summary {
	if a.usageScanner == nil {
		return usage.Summary{Warnings: []string{"usage scanner unavailable"}}
	}
	return a.usageScanner.Scan(sessionID, projectDir)
}
