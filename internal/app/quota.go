package app

import (
	"context"
	"errors"
	"github.com/gp-alex-chen/claude-session-manager/internal/provider"
)

// GetAccountQuota reads the CLI's current login, independently of history and
// token totals. The frontend selects the provider from the focused terminal.
func (a *App) GetAccountQuota(id string, force bool) (provider.QuotaSnapshot, error) {
	if _, err := a.providers.Lookup(provider.ID(id)); err != nil {
		return provider.QuotaSnapshot{}, err
	}
	a.lifecycleMu.Lock()
	ctx := a.ctx
	closed := a.lifecycleID != 0 && a.ctx == nil
	a.lifecycleMu.Unlock()
	if closed {
		return provider.QuotaSnapshot{}, errors.New("应用已关闭")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return a.quotaCache.Read(ctx, provider.ID(id), force), nil
}

// Use the lifecycle context for card queries/mutations, never a model turn.
func (a *App) codexResetService() (context.Context, *provider.ResetCreditService, error) {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if a.ctx == nil && a.lifecycleID != 0 {
		return nil, nil, errors.New("应用已关闭")
	}
	if a.resetCredits == nil || a.resetCreditsEpoch != a.lifecycleID {
		p, err := a.providers.Lookup(provider.Codex)
		if err != nil {
			return nil, nil, err
		}
		capability, ok := p.(provider.ResetCreditProvider)
		if !ok {
			return nil, nil, errors.New("当前 Codex 不支持重置卡")
		}
		a.resetCredits = provider.NewResetCreditService(capability, a.quotaCache)
		a.resetCreditsEpoch = a.lifecycleID
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return ctx, a.resetCredits, nil
}
func (a *App) GetCodexResetCredits() (provider.ResetCreditView, error) {
	ctx, service, err := a.codexResetService()
	if err != nil {
		return provider.ResetCreditView{}, err
	}
	return service.Read(ctx)
}
func (a *App) ConsumeCodexResetCredit(offerToken, creditId string) (provider.ResetCreditRedemption, error) {
	ctx, service, err := a.codexResetService()
	if err != nil {
		return provider.ResetCreditRedemption{}, err
	}
	return service.Consume(ctx, offerToken, creditId)
}
