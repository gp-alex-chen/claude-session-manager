package app

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gp-alex-chen/claude-session-manager/internal/agent"
	"github.com/gp-alex-chen/claude-session-manager/internal/notify"
	"github.com/gp-alex-chen/claude-session-manager/internal/session"
	"github.com/gp-alex-chen/claude-session-manager/internal/state"
	"github.com/gp-alex-chen/claude-session-manager/internal/terminal"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type SessionInfo struct {
	ID   string `json:"id"`
	Dir  string `json:"dir"`
	Name string `json:"name"`
	Time string `json:"time"`
}
type App struct {
	lifecycleMu sync.Mutex
	ctx         context.Context
	terms       *terminal.Manager
	store       *state.Store
	watcher     *agent.Watcher
	lifecycleID uint64
	lookPath    func(string) (string, error)
	debugLog    func(string)
}

func NewApp() *App {
	return NewAppWithStore(state.Default())
}

func NewAppWithStore(store *state.Store) *App {
	if store == nil {
		store = state.Default()
	}
	a := &App{store: store, lookPath: exec.LookPath, debugLog: agent.DebugLog}
	a.terms = terminal.NewManager(terminal.Callbacks{}, a.persistOpenSessions)
	a.terms.SetPersistErrorHandler(func(err error) { a.log("持久化打开会话失败: " + err.Error()) })
	return a
}
func (a *App) startup(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	watcher := agent.NewWatcher(func(list []agent.AgentInfo) {
		runtime.EventsEmit(ctx, "agents:update", list)
	})
	a.terms.SetCallbacks(terminal.Callbacks{
		Data: func(token, data string) { runtime.EventsEmit(ctx, "term:data", token, data) },
		Exit: func(token string) { runtime.EventsEmit(ctx, "term:exit", token) },
	})
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
	a.lifecycleMu.Lock()
	current := a.lifecycleID == lifecycleID && a.watcher == watcher
	if current {
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
	a.terms.CloseAll()
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
	for _, s := range session.ScanAll() {
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
	for _, s := range session.ScanAll() {
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
func (a *App) StartSession(id, dir string) (string, error) {
	if a.terms.IsRunning(id) {
		return id, nil
	}
	if err := a.startPTY(id, a.claudeCmd("-r "+id), dir); err != nil {
		return "", err
	}
	return id, nil
}
func (a *App) StartNew(dir string) (string, error) {
	token := "new-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := a.startPTY(token, a.claudeCmd(""), dir); err != nil {
		return "", err
	}
	return token, nil
}
func (a *App) startPTY(token, cmdLine, dir string) error { return a.terms.Start(token, cmdLine, dir) }
func (a *App) TermWrite(token, b64 string) {
	raw, err := terminal.DecodeInput(b64)
	if err == nil {
		a.terms.Write(token, raw)
	}
}
func (a *App) TermResize(token string, cols, rows int) { a.terms.Resize(token, cols, rows) }
func (a *App) TermKill(token string)                   { a.terms.Kill(token) }
func (a *App) NotifyBeep()                             { notify.Beep() }
func (a *App) persistOpenSessions() error {
	if a.store == nil || a.terms == nil {
		return nil
	}
	return a.store.SaveOpen(a.terms.OpenIDs())
}
func (a *App) claudeCmd(sessionArgs string) string {
	args := strings.TrimSpace(sessionArgs)
	if a.GetShell() == "pwsh" {
		if a.shellAvailable("pwsh") {
			return `pwsh -NoLogo -NoExit -Command "claude ` + args + `"`
		}
		a.DebugLog("pwsh 当前不可用，会话回退 cmd 启动")
	}
	if args == "" {
		return "cmd /c claude"
	}
	return "cmd /c claude " + args
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
