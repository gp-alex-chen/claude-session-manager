package app

import (
	"context"
	"errors"
	"github.com/gp-alex-chen/claude-session-manager/internal/agent"
	"github.com/gp-alex-chen/claude-session-manager/internal/provider"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"path/filepath"
)

func (a *App) newProviderRegistry(withCodex bool) *provider.Registry {
	r := provider.NewRegistry()
	_ = r.Register(&provider.ClaudeAdapter{History: a.sessionSnapshotDetailed, GetStatus: a.claudeAgents, GetUsage: a.claudeUsage,
		GetQuota: func(ctx context.Context) (provider.QuotaSnapshot, error) {
			return provider.ReadClaudeQuota(ctx, a.lookPath)
		},
		RenameNative: func(ctx context.Context, id, name, dir string) error {
			err := provider.RenameClaude(ctx, a.lookPath, id, name, dir)
			if err == nil && a.sessionCatalog != nil {
				a.sessionCatalog.Invalidate()
			}
			return err
		},
	})
	if withCodex {
		lookup := func(name string) (string, error) { return a.lookPath(name) }
		_ = r.Register(provider.NewSharedCodexLive(lookup, a.signalProviderUpdate, a.store))
		userSettings := ""
		runtimeRoot := ""
		if home, err := userHomeDir(); err == nil && home != "" {
			userSettings = filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
			runtimeRoot = filepath.Join(home, ".cache", "TheOne", "antigravity-runtime")
		}
		_ = r.Register(provider.NewAntigravity(provider.AntigravityConfig{
			Lookup: lookup, OnChange: a.signalProviderUpdate,
			UserSettingsPath: userSettings,
			OnError:          func(err error) { a.log("清理 Antigravity 临时文件失败: " + err.Error()) },
			TempRoot:         runtimeRoot,
		}))
	}
	return r
}

func (a *App) startAgentPTY(token string, id provider.ID, ref *provider.SessionRef, dir string) error {
	// Capture startup intent before any newly opened terminal can persist a
	// different live set, including a manually created temporary session.
	a.lifecycleMu.Lock()
	_, startupErr := a.captureStartupRestoreReferencesLocked()
	a.lifecycleMu.Unlock()
	if startupErr != nil {
		a.log("读取启动恢复清单失败: " + startupErr.Error())
	}
	if id == provider.Antigravity {
		return a.startAntigravityPTY(token, ref, dir)
	}
	runtimeCtx := a.runtimeContext()
	a.launchMu.Lock()
	defer a.launchMu.Unlock()
	a.lifecycleMu.Lock()
	closed := a.stopping || (a.lifecycleID != 0 && a.ctx == nil)
	a.lifecycleMu.Unlock()
	if closed {
		return errors.New("应用已关闭，无法启动终端")
	}
	if ref != nil && a.terms.IsRunning(token) {
		if !a.agentRuntimeMatches(token, *ref) {
			return errors.New("此终端已切换会话，请先关闭再恢复原会话")
		}
		return nil
	}
	spec, err := a.providers.PrepareLaunch(runtimeCtx, id, provider.OpenRequest{Ref: ref, Dir: dir, RuntimeToken: token, RuntimeContext: runtimeCtx})
	if err != nil {
		return err
	}
	command, err := a.renderAgentCommand(id, spec)
	if err == nil {
		if spec.Close == nil && len(spec.Env) == 0 {
			err = a.startPTY(token, command, dir)
		} else {
			err = a.terms.StartWithOptions(token, command, dir, spec.Env, spec.Close)
			if err == nil {
				a.clearAdoptionCancellation(token)
			}
		}
	}
	if err != nil && spec.Close != nil {
		spec.Close()
	}
	if err == nil {
		a.noteStartupSessionStarted(token)
	}
	if err == nil && id == provider.Codex {
		// A fast native reply can arrive before Manager registers the PTY.
		a.codexStatusLocked(a.runtimeContext(), false)
		a.signalProviderUpdate()
	}
	return err
}

// The launch lock orders status snapshots with starts and shutdown. Do not
// call this from a transport observer: PTY startup also owns Manager's lock.
func (a *App) codexStatusLocked(ctx context.Context, force bool) []agent.AgentInfo {
	confirmed := map[string]bool{}
	if p, err := a.providers.Lookup(provider.Codex); err == nil {
		if source, ok := p.(provider.RestorableSource); ok {
			ids, err := source.RestorableSessions(ctx, force)
			if err != nil {
				a.log("确认 Codex 原生会话保存失败: " + err.Error())
			}
			for _, id := range ids {
				confirmed["codex:"+id] = true
			}
		}
	}
	list, warnings := a.providers.StatusExcept(ctx, provider.Claude)
	for _, warning := range warnings {
		a.log(warning.Error())
	}
	for i := range list {
		info := &list[i]
		if info.Provider != string(provider.Codex) || info.RuntimeToken == "" || a.terms == nil || !a.terms.IsRunning(info.RuntimeToken) {
			continue
		}
		if !confirmed[info.SessionID] {
			if err := a.terms.UnbindCodexSession(info.RuntimeToken); err != nil {
				a.log("保存 Codex 待确认状态失败: " + err.Error())
			}
			continue
		}
		if err := a.terms.BindCodexSession(info.RuntimeToken, info.SessionID); err != nil {
			if !a.terms.IsSession(info.RuntimeToken, info.SessionID) {
				_ = a.terms.UnbindCodexSession(info.RuntimeToken)
			}
			a.log("绑定 Codex 原生会话失败: " + err.Error())
			continue
		}
		info.Restorable = true
	}
	return list
}

func (a *App) sealProviderRestoration() {
	if p, err := a.providers.Lookup(provider.Codex); err == nil {
		if sealer, ok := p.(provider.RestorationSealer); ok {
			sealer.SealRestoration()
		}
	}
}

func (a *App) beginProviderServices() {
	for _, id := range []provider.ID{provider.Codex, provider.Antigravity} {
		if p, err := a.providers.Lookup(id); err == nil {
			if service, ok := p.(provider.ServiceLifecycle); ok {
				service.BeginService()
			}
		}
	}
}
func (a *App) closeProviderServices() {
	for _, id := range []provider.ID{provider.Codex, provider.Antigravity} {
		if p, err := a.providers.Lookup(id); err == nil {
			if service, ok := p.(provider.ServiceLifecycle); ok {
				service.CloseService()
			}
		}
	}
}

func (a *App) signalProviderUpdate() {
	select {
	case a.statusChanged <- struct{}{}:
	default:
	}
}

func (a *App) watchProviderUpdates(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.statusChanged:
			a.emitAgentUpdate(ctx)
		}
	}
}

func (a *App) emitAgentUpdate(ctx context.Context) {
	// Only the update worker takes this lock; watcher callbacks merely queue.
	// Recheck lifecycle after waiting so an old worker cannot emit after exit.
	a.launchMu.Lock()
	a.lifecycleMu.Lock()
	current := a.ctx == ctx && ctx.Err() == nil
	watcher := a.watcher
	a.lifecycleMu.Unlock()
	var list, others []agent.AgentInfo
	if current && watcher != nil {
		list = watcher.Snapshot()
		others = a.codexStatusLocked(ctx, false)
		list = append(list, others...)
	}
	a.launchMu.Unlock()
	if current && watcher != nil {
		// Only Codex has a verified completion event. Other native providers
		// still update UI/approval attention, without inheriting its source.
		codex := make([]agent.AgentInfo, 0, len(others))
		for _, info := range others {
			if info.Provider == string(provider.Codex) {
				codex = append(codex, info)
			}
		}
		a.observeNotificationSource(ctx, "codex", codex)
		runtime.EventsEmit(ctx, "agents:update", list)
	}
}

// Agent capabilities describe the actual adapters, including native rename.
func (a *App) ListAgentProviders() []provider.Descriptor {
	return a.providers.Descriptors()
}

func (a *App) GetAgents() []agent.AgentInfo {
	list, warnings := a.providers.StatusOnly(a.runtimeContext(), provider.Claude)
	for _, warning := range warnings {
		a.log(warning.Error())
	}
	a.launchMu.Lock()
	others := a.codexStatusLocked(a.runtimeContext(), false)
	a.launchMu.Unlock()
	out := []agent.AgentInfo{}
	for _, info := range list {
		if info.Provider == string(provider.Claude) {
			out = append(out, info)
		}
	}
	return append(out, others...)
}
