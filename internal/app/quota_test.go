package app

import (
	"context"
	"github.com/gp-alex-chen/claude-session-manager/internal/provider"
	"github.com/gp-alex-chen/claude-session-manager/internal/state"
	"testing"
)

func TestAccountQuotaBindingValidatesCachesAndStopsAfterShutdown(t *testing.T) {
	a := NewAppWithStore(state.NewStore(t.TempDir()))
	calls := 0
	a.quotaCache = provider.NewQuotaCache(func(_ context.Context, id provider.ID) (provider.QuotaSnapshot, error) {
		calls++
		return provider.QuotaSnapshot{Status: "available", Windows: []provider.QuotaWindow{{ID: "session", UsedPercent: 15, WindowMinutes: 300}}}, nil
	})
	if _, err := a.GetAccountQuota("unknown", false); err == nil || calls != 0 {
		t.Fatal("invalid provider queried")
	}
	for i := 0; i < 2; i++ {
		q, err := a.GetAccountQuota("claude", true)
		if err != nil || q.Provider != provider.Claude || len(q.Windows) != 1 {
			t.Fatal(q, err)
		}
	}
	if calls != 1 {
		t.Fatal("cache bypassed", calls)
	}
	a.lifecycleID = 1
	if _, err := a.GetAccountQuota("claude", false); err == nil {
		t.Fatal("query allowed after shutdown")
	}
}

func TestShutdownCancelsInFlightAccountQuota(t *testing.T) {
	a := NewAppWithStore(state.NewStore(t.TempDir()))
	a.providers = provider.NewRegistry()
	a.providers.Register(&provider.ClaudeAdapter{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.ctx = ctx
	a.cancelLifecycle = cancel
	a.lifecycleID = 1
	entered := make(chan struct{})
	a.quotaCache = provider.NewQuotaCache(func(ctx context.Context, _ provider.ID) (provider.QuotaSnapshot, error) {
		close(entered)
		<-ctx.Done()
		return provider.QuotaSnapshot{}, ctx.Err()
	})
	done := make(chan provider.QuotaSnapshot, 1)
	go func() { q, _ := a.GetAccountQuota("claude", false); done <- q }()
	<-entered
	a.shutdown(context.Background())
	q := <-done
	if q.Status != "error" || len(q.Windows) != 0 {
		t.Fatal(q)
	}
}
