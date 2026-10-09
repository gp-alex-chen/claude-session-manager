package app

import (
	"context"
	"errors"
	"github.com/gp-alex-chen/claude-session-manager/internal/agent"
	"github.com/gp-alex-chen/claude-session-manager/internal/provider"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) newProviderRegistry(withCodex bool) *provider.Registry {
	r := provider.NewRegistry()
	_ = r.Register(&provider.ClaudeAdapter{History: a.sessionSnapshotDetailed, GetStatus: a.claudeAgents, GetUsage: a.claudeUsage,
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
		_ = r.Register(provider.NewCodexLive(provider.NewCodex(provider.NewCodexRPC(lookup)), provider.NewCodexRelayFactory(lookup), a.signalProviderUpdate))
	}
	return r
}

func (a *App) startAgentPTY(token string, id provider.ID, ref *provider.SessionRef, dir string) error {
	a.launchMu.Lock()
	defer a.launchMu.Unlock()
	a.lifecycleMu.Lock()
	closed := a.lifecycleID != 0 && a.ctx == nil
	a.lifecycleMu.Unlock()
	if closed {
		return errors.New("应用已关闭，无法启动终端")
	}
	if ref != nil && a.terms.IsRunning(token) {
		return nil
	}
	spec, err := a.providers.PrepareLaunch(a.runtimeContext(), id, provider.OpenRequest{Ref: ref, Dir: dir, RuntimeToken: token})
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
	return err
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
	a.lifecycleMu.Lock()
	current := a.ctx == ctx && ctx.Err() == nil
	watcher := a.watcher
	a.lifecycleMu.Unlock()
	if current && watcher != nil {
		list := watcher.Snapshot()
		others, _ := a.providers.StatusExcept(ctx, provider.Claude)
		list = append(list, others...)
		runtime.EventsEmit(ctx, "agents:update", list)
	}
}

// Agent capabilities describe the actual adapters, including native rename.
func (a *App) ListAgentProviders() []provider.Descriptor {
	return a.providers.Descriptors()
}

func (a *App) GetAgents() []agent.AgentInfo {
	list, warnings := a.providers.Status(a.runtimeContext())
	for _, warning := range warnings {
		a.log(warning.Error())
	}
	return list
}
