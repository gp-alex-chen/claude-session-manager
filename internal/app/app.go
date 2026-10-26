package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gp-alex-chen/claude-session-manager/internal/agent"
	"github.com/gp-alex-chen/claude-session-manager/internal/notify"
	"github.com/gp-alex-chen/claude-session-manager/internal/provider"
	"github.com/gp-alex-chen/claude-session-manager/internal/session"
	"github.com/gp-alex-chen/claude-session-manager/internal/state"
	"github.com/gp-alex-chen/claude-session-manager/internal/terminal"
	"github.com/gp-alex-chen/claude-session-manager/internal/usage"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type SessionInfo struct {
	ID       string `json:"id"`
	Dir      string `json:"dir"`
	Name     string `json:"name"`
	Time     string `json:"time"`
	Provider string `json:"provider"`
	RawID    string `json:"rawId"`
}

type pendingAdoption struct {
	id        string
	count     int
	cancelled atomic.Bool
}

type App struct {
	launchMu            sync.Mutex
	renameMu            sync.Mutex
	lifecycleMu         sync.Mutex
	notificationMu      sync.Mutex
	adoptionMu          sync.Mutex
	ctx                 context.Context
	cancelLifecycle     context.CancelFunc
	terms               *terminal.Manager
	store               *state.Store
	watcher             *agent.Watcher
	lifecycleID         uint64
	lookPath            func(string) (string, error)
	debugLog            func(string)
	startPTYFn          func(string, string, string) error
	chooseDirFn         func(context.Context, runtime.OpenDialogOptions) (string, error)
	usageScanner        *usage.Scanner
	providers           *provider.Registry
	quotaCache          *provider.QuotaCache
	completionBadge     *notify.TaskbarBadge
	notificationProgram *notify.ProgramService
	sessionCatalog      *session.Catalog
	pendingAdoptions    map[string]*pendingAdoption
	cancelledAdoptions  map[string]struct{}
	statusChanged       chan struct{}
}

func NewApp() *App {
	return NewAppWithStore(state.Default())
}

func NewAppWithStore(store *state.Store) *App {
	if store == nil {
		store = state.Default()
	}
	a := &App{
		store:              store,
		lookPath:           exec.LookPath,
		debugLog:           agent.DebugLog,
		chooseDirFn:        runtime.OpenDirectoryDialog,
		usageScanner:       defaultUsageScanner(),
		sessionCatalog:     defaultSessionCatalog(),
		pendingAdoptions:   make(map[string]*pendingAdoption),
		cancelledAdoptions: make(map[string]struct{}),
		statusChanged:      make(chan struct{}, 1),
	}
	a.terms = terminal.NewManager(terminal.Callbacks{}, func(ids []string) error {
		return a.store.SaveOpen(ids)
	})
	a.terms.SetPersistErrorHandler(func(err error) { a.log("持久化打开会话失败: " + err.Error()) })
	a.providers = a.newProviderRegistry(true)
	a.quotaCache = provider.NewQuotaCache(func(ctx context.Context, id provider.ID) (provider.QuotaSnapshot, error) {
		return a.providers.AccountQuota(ctx, id)
	})
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
	ctx, cancel := context.WithCancel(ctx)
	watcher := agent.NewWatcherWithObserver(func([]agent.AgentInfo) {
		a.signalProviderUpdate()
	}, func(list []agent.AgentInfo) { a.observeNotificationSource(ctx, "claude", list) })
	// Register the candidate before stopping the old watcher. Stop may wait on
	// its fetch loop, so it must never run while lifecycleMu is held.
	a.lifecycleMu.Lock()
	old := a.watcher
	oldCancel := a.cancelLifecycle
	oldNotifications := a.notificationProgram
	a.notificationProgram = nil
	a.lifecycleID++
	lifecycleID := a.lifecycleID
	a.ctx = ctx
	a.cancelLifecycle = cancel
	a.watcher = watcher
	a.lifecycleMu.Unlock()
	if oldCancel != nil {
		oldCancel()
	}
	if oldNotifications != nil {
		oldNotifications.Close()
	}
	if old != nil {
		old.Stop()
	}
	// Recheck the generation before installing callbacks or starting the
	// candidate. A concurrent Startup/Shutdown may have made it stale.
	a.notificationMu.Lock()
	notificationConfig, configErr := a.store.NotificationConfig()
	if configErr != nil {
		notificationConfig = state.DefaultNotificationConfig()
	}
	a.lifecycleMu.Lock()
	current := a.lifecycleID == lifecycleID && a.watcher == watcher
	if current {
		a.beginProviderServices()
		a.notificationProgram = notify.NewProgramService(ctx, notificationConfig)
		if a.completionBadge != nil {
			a.completionBadge.Close()
		}
		a.completionBadge = notify.NewTaskbarBadge()
		go a.completionBadge.Run(ctx)
		a.terms.SetCallbacks(terminal.Callbacks{
			Data: func(token, data string) { runtime.EventsEmit(ctx, "term:data", token, data) },
			Exit: func(token string) { runtime.EventsEmit(ctx, "term:exit", token) },
		})
		watcher.Start(ctx)
		go a.watchProviderUpdates(ctx)
		a.signalProviderUpdate()
	}
	a.lifecycleMu.Unlock()
	a.notificationMu.Unlock()
	if !current {
		cancel()
	}
}
func (a *App) shutdown(context.Context) {
	a.launchMu.Lock()
	var notifications *notify.ProgramService
	defer func() {
		a.launchMu.Unlock()
		if notifications != nil {
			notifications.Close()
		}
	}()
	// Stop delivery before shutdown snapshots or terminal exits can be observed.
	a.lifecycleMu.Lock()
	notifications = a.notificationProgram
	a.notificationProgram = nil
	a.lifecycleMu.Unlock()
	if notifications != nil {
		notifications.Stop()
	}
	// Capture native identities even when the UI has never queried agents.
	a.sealProviderRestoration()
	a.codexStatusLocked(context.Background(), true)
	a.lifecycleMu.Lock()
	watcher := a.watcher
	cancel := a.cancelLifecycle
	a.lifecycleID++
	a.watcher = nil
	a.ctx = nil
	a.cancelLifecycle = nil
	badge := a.completionBadge
	a.completionBadge = nil
	a.lifecycleMu.Unlock()
	if badge != nil {
		badge.Close()
	}
	if watcher != nil {
		watcher.Stop()
	}
	a.closeAllTerms()
	a.closeProviderServices()
	if cancel != nil {
		cancel()
	}
}
func (a *App) Startup(ctx context.Context)  { a.startup(ctx) }
func (a *App) Shutdown(ctx context.Context) { a.shutdown(ctx) }
func (a *App) ListSessions() []SessionInfo {
	return a.listProviderSessions(false, nil).Sessions
}

type ProviderSessionsResult struct {
	Sessions           []SessionInfo `json:"sessions"`
	SucceededProviders []string      `json:"succeededProviders"`
	Warnings           []string      `json:"warnings"`
}

func (a *App) ListSessionsForProviders(ids []string) (ProviderSessionsResult, error) {
	return a.filteredProviderSessions(false, ids)
}
func (a *App) ListHiddenSessionsForProviders(ids []string) (ProviderSessionsResult, error) {
	return a.filteredProviderSessions(true, ids)
}
func (a *App) filteredProviderSessions(hidden bool, ids []string) (ProviderSessionsResult, error) {
	if len(ids) == 0 {
		return ProviderSessionsResult{}, errors.New("至少选择一种会话来源")
	}
	out := make([]provider.ID, 0, len(ids))
	seen := make(map[string]bool)
	for _, id := range ids {
		if _, err := a.providers.Lookup(provider.ID(id)); err != nil {
			return ProviderSessionsResult{}, err
		}
		if !seen[id] {
			out = append(out, provider.ID(id))
			seen[id] = true
		}
	}
	return a.listProviderSessions(hidden, out), nil
}
func (a *App) listProviderSessions(wantHidden bool, ids []provider.ID) ProviderSessionsResult {
	result := ProviderSessionsResult{Sessions: []SessionInfo{}, SucceededProviders: []string{}, Warnings: []string{}}
	st, err := a.store.Load()
	if err != nil {
		a.DebugLog("读取 favorites.json 失败: " + err.Error())
	}
	hidden, aliases := st.HiddenSet(), st.Aliases
	out := []SessionInfo{}
	list, succeeded, warnings := a.providers.QuerySessions(a.runtimeContext(), ids)
	for _, id := range succeeded {
		result.SucceededProviders = append(result.SucceededProviders, string(id))
	}
	for _, warning := range warnings {
		a.log("读取会话目录失败: " + warning.Error())
		result.Warnings = append(result.Warnings, warning.Error())
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Time.After(list[j].Time) })
	for _, s := range list {
		key, err := provider.EncodeKey(s.Ref)
		if err != nil {
			continue
		}
		if hidden[key] != wantHidden {
			continue
		}
		name := s.Name
		p, _ := a.providers.Lookup(s.Ref.Provider)
		_, nativeRename := p.(provider.RenameProvider)
		if n := aliases[key]; n != "" && !nativeRename {
			name = n
		}
		out = append(out, SessionInfo{ID: key, Dir: s.Dir, Name: name, Time: s.Time.Format("01-02 15:04"), Provider: string(s.Ref.Provider), RawID: s.Ref.ID})
	}
	result.Sessions = out
	return result
}
func (a *App) ListHiddenSessions() []SessionInfo {
	return a.listProviderSessions(true, nil).Sessions
}
func (a *App) sessionSnapshot() []*session.Session {
	snapshot, err := a.sessionSnapshotDetailed()
	if err != nil {
		a.log("读取会话目录失败: " + err.Error())
	}
	return snapshot
}
func (a *App) sessionSnapshotDetailed() ([]*session.Session, error) {
	if a.sessionCatalog == nil {
		return nil, errors.New("Claude 会话目录不可用")
	}
	snapshot, warnings := a.sessionCatalog.SnapshotWithWarnings()
	if len(warnings) != 0 {
		return snapshot, errors.New(strings.Join(warnings, "; "))
	}
	return snapshot, nil
}
func (a *App) StartSession(id, dir string) (string, error) {
	ref, err := provider.DecodeKey(id)
	if err != nil {
		return "", err
	}
	if a.terms.IsRunning(id) {
		if !a.terms.IsSession(id, id) {
			return "", errors.New("此终端已切换到其他 Codex 会话，请先关闭该终端再恢复原会话")
		}
		return id, nil
	}
	if err := a.startAgentPTY(id, ref.Provider, &ref, dir); err != nil {
		return "", err
	}
	return id, nil
}
func (a *App) StartNew(dir string) (string, error) {
	return a.StartNewAgent(string(provider.Claude), dir)
}
func (a *App) StartNewAgent(agentID, dir string) (string, error) {
	token := "new-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if agentID != string(provider.Claude) {
		token = "new-" + agentID + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	if err := a.startAgentPTY(token, provider.ID(agentID), nil, dir); err != nil {
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
func (a *App) NotifyBeep() { notify.Beep() }
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
	var ref *provider.SessionRef
	if sessionID != "" {
		ref = &provider.SessionRef{Provider: provider.Claude, ID: sessionID}
	}
	return a.providerCommand(provider.Claude, ref, "")
}
func (a *App) providerCommand(agentID provider.ID, ref *provider.SessionRef, dir string) (string, error) {
	spec, err := a.providers.PrepareLaunch(a.runtimeContext(), agentID, provider.OpenRequest{Ref: ref, Dir: dir})
	if err != nil {
		return "", err
	}
	return a.renderAgentCommand(agentID, spec)
}

func (a *App) renderAgentCommand(agentID provider.ID, spec provider.LaunchSpec) (string, error) {
	// Preserve legacy Claude startup; new providers fail before a PTY is
	// created when their executable is unavailable.
	if agentID != provider.Claude {
		if agentID == provider.Codex && spec.Executable != "" {
			st, err := os.Stat(spec.Executable)
			if !filepath.IsAbs(spec.Executable) || err != nil || st.IsDir() {
				return "", errors.New("已解析的 Codex 程序不可用，请检查安装或重新打开终端")
			}
		} else if _, err := a.lookPath(spec.Command); err != nil {
			return "", fmt.Errorf("未检测到 %s CLI，请先安装并加入 PATH", agentID)
		}
	}
	command := spec.Command
	if len(spec.Args) > 0 {
		command += " " + strings.Join(spec.Args, " ")
	}
	if a.GetShell() == "pwsh" {
		if a.shellAvailable("pwsh") {
			if len(spec.Args) == 0 {
				command += " "
			}
			return `pwsh -NoLogo -NoExit -Command "` + command + `"`, nil
		}
		a.DebugLog("pwsh 当前不可用，会话回退 cmd 启动")
	}
	return "cmd /c " + command, nil
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
func (a *App) claudeAgents(context.Context) []agent.AgentInfo {
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
	ref := provider.SessionRef{Provider: provider.Claude, ID: sessionID}
	if sessionID != "" {
		var err error
		if strings.HasPrefix(sessionID, "new-codex-") {
			ref = provider.SessionRef{Provider: provider.Codex, ID: sessionID}
		} else {
			ref, err = provider.DecodeKey(sessionID)
		}
		if err != nil {
			return usage.Summary{Warnings: []string{err.Error()}}
		}
	}
	return a.providers.Usage(a.runtimeContext(), ref, projectDir)
}
func (a *App) claudeUsage(sessionID, projectDir string) usage.Summary {
	if a.usageScanner == nil {
		return usage.Summary{Warnings: []string{"usage scanner unavailable"}}
	}
	return a.usageScanner.Scan(sessionID, projectDir)
}
