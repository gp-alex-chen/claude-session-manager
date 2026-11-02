package app

import (
	"context"
	"errors"
	"github.com/gp-alex-chen/claude-session-manager/internal/provider"
	"github.com/gp-alex-chen/claude-session-manager/internal/state"
	"sync"
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

func TestResetCreditBindingsRejectOldOffersAfterShutdown(t *testing.T) {
	a := NewAppWithStore(state.NewStore(t.TempDir()))
	a.lifecycleID = 1
	if _, err := a.GetCodexResetCredits(); err == nil {
		t.Fatal("read allowed after shutdown")
	}
	if _, err := a.ConsumeCodexResetCredit("old-offer", "fixture-credit"); err == nil {
		t.Fatal("consume allowed after shutdown")
	}
}

type appResetFixture struct {
	*provider.CodexAdapter
	mu    sync.Mutex
	calls int
	read  func(context.Context) (provider.QuotaSnapshot, error)
}

func (p *appResetFixture) ReadResetCredits(ctx context.Context) (provider.QuotaSnapshot, error) {
	if p.read != nil {
		return p.read(ctx)
	}
	return provider.QuotaSnapshot{Provider: provider.Codex, Status: "available", AccountKey: "fixture-account", ResetCredits: &provider.ResetCreditsSummary{AvailableCount: 1, Credits: nil}}, nil
}
func (p *appResetFixture) RedeemResetCredit(context.Context, string, string, string, bool) (provider.ResetCreditRedemption, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	q := provider.QuotaSnapshot{Status: "available", Windows: []provider.QuotaWindow{{UsedPercent: 0}}, ResetCredits: &provider.ResetCreditsSummary{AvailableCount: 0}}
	return provider.ResetCreditRedemption{Outcome: "reset", Snapshot: &q}, nil
}
func TestResetCreditBindingsShareCacheAndInvalidateOffersOnLifecycleChange(t *testing.T) {
	a := NewAppWithStore(state.NewStore(t.TempDir()))
	fake := &appResetFixture{CodexAdapter: provider.NewCodex(nil)}
	a.providers = provider.NewRegistry()
	a.providers.Register(fake)
	view, err := a.GetCodexResetCredits()
	if err != nil || view.OfferToken == "" {
		t.Fatal(view, err)
	}
	result, err := a.ConsumeCodexResetCredit(view.OfferToken, "")
	if err != nil || result.Outcome != "reset" {
		t.Fatal(result, err)
	}
	cached, _ := a.GetAccountQuota("codex", false)
	if cached.ResetCredits.AvailableCount != 0 {
		t.Fatal(cached)
	}
	// A new lifecycle cannot reuse any previous confirmation token.
	a.lifecycleMu.Lock()
	a.lifecycleID++
	a.ctx = context.Background()
	a.lifecycleMu.Unlock()
	if _, err = a.ConsumeCodexResetCredit(view.OfferToken, ""); err == nil {
		t.Fatal("old confirmation accepted")
	}
	if fake.calls != 1 {
		t.Fatal("old offer consumed again")
	}
}
func TestResetCreditBindingUsesCancellableLifecycleContext(t *testing.T) {
	a := NewAppWithStore(state.NewStore(t.TempDir()))
	entered := make(chan struct{})
	fake := &appResetFixture{CodexAdapter: provider.NewCodex(nil), read: func(ctx context.Context) (provider.QuotaSnapshot, error) {
		close(entered)
		<-ctx.Done()
		return provider.QuotaSnapshot{}, ctx.Err()
	}}
	a.providers = provider.NewRegistry()
	a.providers.Register(fake)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.ctx = ctx
	a.lifecycleID = 1
	done := make(chan error, 1)
	go func() { _, err := a.GetCodexResetCredits(); done <- err }()
	<-entered
	cancel()
	if err := <-done; err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
