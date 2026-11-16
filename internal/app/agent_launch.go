package app

import (
	"context"
	"errors"

	"github.com/gp-alex-chen/claude-session-manager/internal/provider"
)

type pendingAgentLaunch struct {
	generation  uint64
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	predecessor <-chan struct{}
}

func (a *App) startAntigravityPTY(token string, ref *provider.SessionRef, dir string) error {
	return a.startAntigravityPTYWithHistory(token, ref, dir, nil, nil)
}

func (a *App) startAntigravityPTYWithHistory(token string, ref *provider.SessionRef, dir string, history provider.StartupHistory, preparation context.Context) error {
	a.lifecycleMu.Lock()
	generation, runtimeCtx, stopping := a.lifecycleID, a.ctx, a.stopping
	a.lifecycleMu.Unlock()
	if stopping || (generation != 0 && runtimeCtx == nil) {
		return errors.New("应用已关闭，无法启动终端")
	}
	if runtimeCtx == nil {
		runtimeCtx = context.Background()
	}
	launchCtx := runtimeCtx
	if preparation != nil {
		launchCtx = preparation
	}
	pending, err := a.beginAgentLaunch(token, launchCtx, generation)
	if err != nil {
		return err
	}
	defer a.finishAgentLaunch(token, pending)
	if pending.predecessor != nil {
		select {
		case <-pending.predecessor:
		case <-pending.ctx.Done():
			return pending.ctx.Err()
		}
	}
	current := func() bool {
		a.lifecycleMu.Lock()
		defer a.lifecycleMu.Unlock()
		return a.lifecycleID == generation && !a.stopping && pending.ctx.Err() == nil && (generation == 0 || a.ctx != nil)
	}
	// Reopening an already confirmed runtime requires no metadata query.
	a.launchMu.Lock()
	if !current() {
		a.launchMu.Unlock()
		return errors.New("终端启动已取消")
	}
	if ref != nil && a.terms.IsRunning(token) {
		matches := a.agentRuntimeMatches(token, *ref)
		a.launchMu.Unlock()
		if !matches {
			return errors.New("此终端已切换会话，请先关闭再恢复原会话")
		}
		return nil
	}
	a.launchMu.Unlock()

	// ACP metadata can be slow or ignore cancellation. It must not hold up
	// independent launches, status delivery or shutdown.
	req := provider.OpenRequest{Ref: ref, Dir: dir, RuntimeToken: token, RuntimeContext: runtimeCtx}
	var spec provider.LaunchSpec
	if history != nil {
		spec, err = history.PrepareLaunch(pending.ctx, req)
	} else {
		spec, err = a.providers.PrepareLaunch(pending.ctx, provider.Antigravity, req)
	}
	if err != nil {
		return err
	}
	transferred := false
	defer func() {
		if !transferred && spec.Close != nil {
			spec.Close()
		}
	}()
	command, err := a.renderAgentCommand(provider.Antigravity, spec)
	if err != nil {
		return err
	}
	a.launchMu.Lock()
	defer a.launchMu.Unlock()
	if !current() {
		return errors.New("终端启动已取消")
	}
	err = a.commitAgentLaunch(token, pending, func() error {
		err := a.terms.StartUnboundWithOptions(token, command, dir, spec.Env, spec.Close)
		if err == nil {
			a.noteStartupSessionStarted(token)
		}
		return err
	})
	transferred = err == nil
	return err
}

func (a *App) beginAgentLaunch(token string, parent context.Context, generations ...uint64) (*pendingAgentLaunch, error) {
	ctx, cancel := context.WithCancel(parent)
	var generation uint64
	if len(generations) > 0 {
		generation = generations[0]
	} else {
		a.lifecycleMu.Lock()
		generation = a.lifecycleID
		a.lifecycleMu.Unlock()
	}
	a.agentLaunchMu.Lock()
	defer a.agentLaunchMu.Unlock()
	if a.agentLaunches == nil {
		a.agentLaunches = make(map[string]*pendingAgentLaunch)
	}
	old := a.agentLaunches[token]
	if old != nil && old.ctx.Err() == nil {
		cancel()
		return nil, errors.New("该终端正在启动，请稍候")
	}
	pending := &pendingAgentLaunch{generation: generation, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	if old != nil {
		pending.predecessor = old.done
	}
	a.agentLaunches[token] = pending
	return pending, nil
}

func (a *App) finishAgentLaunch(token string, pending *pendingAgentLaunch) {
	pending.cancel()
	// Keep cleanup ordered across canceled replacements of the same token.
	// No app lock is held while waiting; close can still cancel the new owner.
	if pending.predecessor != nil {
		<-pending.predecessor
	}
	a.agentLaunchMu.Lock()
	defer a.agentLaunchMu.Unlock()
	if a.agentLaunches[token] == pending {
		delete(a.agentLaunches, token)
	}
	close(pending.done)
}

func (a *App) commitAgentLaunch(token string, pending *pendingAgentLaunch, start func() error) error {
	a.agentLaunchMu.Lock()
	defer a.agentLaunchMu.Unlock()
	if a.agentLaunches[token] != pending || pending.ctx.Err() != nil {
		return errors.New("终端启动已取消")
	}
	return start()
}

func (a *App) killAgentRuntime(token string) {
	// Serialize the final PTY registration with close. Closing before commit
	// cancels preparation; closing after commit tears down that exact runtime.
	a.agentLaunchMu.Lock()
	defer a.agentLaunchMu.Unlock()
	a.revokeSessionRestore(token)
	if pending := a.agentLaunches[token]; pending != nil {
		pending.cancel()
	}
	if a.terms != nil {
		a.terms.Kill(token)
	}
}

func (a *App) cancelAgentLaunches(generation uint64) {
	a.invalidateSessionRestoreGenerations(generation, false, false)
	a.agentLaunchMu.Lock()
	defer a.agentLaunchMu.Unlock()
	for _, pending := range a.agentLaunches {
		if pending.generation == generation {
			pending.cancel()
		}
	}
}

func (a *App) agentRuntimeMatches(token string, ref provider.SessionRef) bool {
	if ref.Provider != provider.Antigravity {
		return a.terms.IsSession(token, token)
	}
	p, err := a.providers.Lookup(provider.Antigravity)
	if err != nil {
		return false
	}
	runtime, ok := p.(interface{ RuntimeMatches(string, string) bool })
	return ok && runtime.RuntimeMatches(token, ref.ID)
}
