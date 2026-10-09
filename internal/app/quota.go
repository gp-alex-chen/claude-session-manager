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
