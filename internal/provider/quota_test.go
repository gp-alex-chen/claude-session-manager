package provider

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestQuotaCacheThrottleStaleAndAccountChanges(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	calls := 0
	fail := false
	status := "available"
	cache := NewQuotaCache(func(context.Context, ID) (QuotaSnapshot, error) {
		calls++
		if fail {
			return QuotaSnapshot{}, errors.New("network")
		}
		return QuotaSnapshot{Status: status, Windows: []QuotaWindow{{ID: "five_hour", UsedPercent: 20, WindowMinutes: 300}}}, nil
	})
	cache.now = func() time.Time { return now }
	first := cache.Read(context.Background(), Codex, false)
	if first.Status != "available" || first.ReadAt.IsZero() {
		t.Fatal(first)
	}
	cache.Read(context.Background(), Codex, true)
	if calls != 1 {
		t.Fatal("force bypassed minimum interval", calls)
	}
	now = now.Add(31 * time.Second)
	fail = true
	stale := cache.Read(context.Background(), Codex, true)
	if !stale.Stale || len(stale.Windows) != 1 || stale.ReadAt != first.ReadAt {
		t.Fatal(stale)
	}
	now = now.Add(31 * time.Second)
	fail = false
	status = "signed_out"
	signedOut := cache.Read(context.Background(), Codex, true)
	if signedOut.Stale || len(signedOut.Windows) != 0 {
		t.Fatal("old account quota retained", signedOut)
	}
}

func TestCodexQuotaUsesAccountMethodsAndPrefersBuckets(t *testing.T) {
	f := &rpcFixture{Reader: strings.NewReader(`{"id":1,"result":{}}
{"id":2,"result":{"account":{"type":"chatgpt","planType":"plus"}}}
{"id":3,"result":{"rateLimits":{"primary":{"usedPercent":99,"windowDurationMins":300}},"rateLimitsByLimitId":{"codex":{"primary":{"usedPercent":22,"windowDurationMins":300,"resetsAt":100},"secondary":{"usedPercent":58,"windowDurationMins":10080}},"codex_other":{"limitName":"Other","primary":{"usedPercent":0,"windowDurationMins":60}}}}}
`)}
	c := &CodexRPC{Open: func(context.Context) (io.ReadWriteCloser, error) { return f, nil }}
	q, err := c.AccountQuota(context.Background())
	if err != nil || q.Plan != "plus" || len(q.Windows) != 3 || q.Windows[0].UsedPercent != 22 || q.Windows[1].WindowMinutes != 10080 || q.Windows[2].UsedPercent != 0 {
		t.Fatal(q, err)
	}
	if !f.closed {
		t.Fatal("transport not closed")
	}
	for _, m := range f.requests {
		if strings.HasPrefix(m["method"].(string), "thread/") || strings.HasPrefix(m["method"].(string), "turn/") {
			t.Fatal(m)
		}
	}
	if f.requests[2]["params"].(map[string]any)["refreshToken"] != false {
		t.Fatal("forced token refresh")
	}
}

func TestCodexQuotaDoesNotQueryNonSubscriptionAccounts(t *testing.T) {
	for _, account := range []string{`null`, `{"type":"apiKey"}`} {
		f := &rpcFixture{Reader: strings.NewReader("{\"id\":1,\"result\":{}}\n{\"id\":2,\"result\":{\"account\":" + account + "}}\n")}
		c := &CodexRPC{Open: func(context.Context) (io.ReadWriteCloser, error) { return f, nil }}
		q, err := c.AccountQuota(context.Background())
		if err != nil || q.Status == "available" || len(f.requests) != 3 || len(q.Windows) != 0 {
			t.Fatal(q, err, f.requests)
		}
		if (account == `{"type":"apiKey"}`) != (q.AuthMode == "api") {
			t.Fatal("API auth must be explicit", q)
		}
	}
}

func TestCodexQuotaLegacyDefaultSurvivesScopedBuckets(t *testing.T) {
	f := &rpcFixture{Reader: strings.NewReader(`{"id":1,"result":{}}
{"id":2,"result":{"account":{"type":"chatgpt","planType":"plus"}}}
{"id":3,"result":{"rateLimits":{"primary":{"usedPercent":10,"windowDurationMins":300},"secondary":{"usedPercent":20,"windowDurationMins":10080}},"rateLimitsByLimitId":{"other":{"primary":{"usedPercent":30,"windowDurationMins":300}}}}}
`)}
	c := &CodexRPC{Open: func(context.Context) (io.ReadWriteCloser, error) { return f, nil }}
	q, err := c.AccountQuota(context.Background())
	if err != nil || len(q.Windows) != 3 || q.Windows[0].Scope != "" || q.Windows[1].Scope != "" || q.Windows[2].Scope == "" {
		t.Fatal(q, err)
	}
}

func TestClaudeQuotaMissingAndNullNeverBecomeZero(t *testing.T) {
	q, err := parseClaudeQuota([]byte(`{"subscription_type":"max","rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":0,"resets_at":"2026-01-01T04:00:00Z"},"seven_day":{"utilization":null}}}`))
	if err != nil || len(q.Windows) != 1 || q.Windows[0].UsedPercent != 0 || q.Windows[0].ResetsAt == 0 {
		t.Fatal(q, err)
	}
	q, err = parseClaudeQuota([]byte(`{"subscription_type":null,"rate_limits_available":false,"rate_limits":null}`))
	if err != nil || q.Status != "unsupported" || len(q.Windows) != 0 {
		t.Fatal(q, err)
	}
	if q.AuthMode == "api" {
		t.Fatal("unavailable allowance misclassified as API")
	}
	q, err = parseClaudeQuota([]byte(`{"auth_mode":"api","subscription_type":null,"rate_limits_available":false,"rate_limits":null}`))
	if err != nil || q.AuthMode != "api" || q.Status != "unsupported" {
		t.Fatal(q, err)
	}
	if _, err = parseClaudeQuota([]byte(`{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":-1}}}`)); err == nil {
		t.Fatal("invalid reading accepted")
	}
}

func TestClaudeQuotaSupportsAdditiveModelScopes(t *testing.T) {
	q, err := parseClaudeQuota([]byte(`{"subscription_type":"max","rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":12},"seven_day":{"utilization":34},"model_scoped":[{"display_name":"Other Model","utilization":56,"resets_at":null}],"extra_usage":{"is_enabled":true,"utilization":23}}}`))
	if err != nil || len(q.Windows) != 3 || q.Windows[2].Scope != "Other Model" {
		t.Fatal(q, err)
	}
}

func TestQuotaCacheConcurrentReadsCoalesceAndReturnIsolatedValues(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	calls := 0
	c := NewQuotaCache(func(context.Context, ID) (QuotaSnapshot, error) {
		calls++
		close(entered)
		<-release
		return QuotaSnapshot{Status: "available", Windows: []QuotaWindow{{ID: "session", UsedPercent: 50}}}, nil
	})
	var wg sync.WaitGroup
	results := make(chan QuotaSnapshot, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- c.Read(context.Background(), Claude, false) }()
	}
	<-entered
	close(release)
	wg.Wait()
	close(results)
	if calls != 1 {
		t.Fatal(calls)
	}
	for q := range results {
		q.Windows[0].UsedPercent = 100
	}
	if q := c.Read(context.Background(), Claude, false); q.Windows[0].UsedPercent != 50 {
		t.Fatal("cache mutated by caller", q)
	}
}

func TestQuotaCacheAPIModeOnlyRefreshesManually(t *testing.T) {
	now := time.Now()
	calls := 0
	mode := "api"
	fail := false
	c := NewQuotaCache(func(context.Context, ID) (QuotaSnapshot, error) {
		calls++
		if fail {
			return QuotaSnapshot{}, errors.New("network")
		}
		return QuotaSnapshot{Status: "unsupported", AuthMode: mode}, nil
	})
	c.now = func() time.Time { return now }
	c.Read(context.Background(), Codex, false)
	now = now.Add(time.Hour)
	c.Read(context.Background(), Codex, false)
	if calls != 1 {
		t.Fatal("API quota queried automatically", calls)
	}
	fail = true
	q := c.Read(context.Background(), Codex, true)
	if q.AuthMode != "api" || q.Status != "unsupported" || !q.Stale {
		t.Fatal("manual failure lost confirmed API mode", q)
	}
	now = now.Add(time.Hour)
	c.Read(context.Background(), Codex, false)
	if calls != 2 {
		t.Fatal(calls)
	}
	fail = false
	mode = "unknown"
	c.Read(context.Background(), Codex, true)
	now = now.Add(15 * time.Minute)
	c.Read(context.Background(), Codex, false)
	if calls != 4 {
		t.Fatal("manual auth change did not resume normal queries", calls)
	}
}

func TestQuotaCacheUsesFifteenMinutesForAllAutomaticReads(t *testing.T) {
	for _, id := range []ID{Claude, Codex} {
		for _, status := range []string{"available", "error"} {
			now := time.Now()
			calls := 0
			c := NewQuotaCache(func(context.Context, ID) (QuotaSnapshot, error) { calls++; return QuotaSnapshot{Status: status}, nil })
			c.now = func() time.Time { return now }
			c.Read(context.Background(), id, false)
			now = now.Add(15*time.Minute - time.Millisecond)
			c.Read(context.Background(), id, false)
			if calls != 1 {
				t.Fatal(id, status, "refreshed before fifteen minutes", calls)
			}
			now = now.Add(time.Millisecond)
			c.Read(context.Background(), id, false)
			if calls != 2 {
				t.Fatal("did not refresh at fifteen minutes", calls)
			}
			now = now.Add(31 * time.Second)
			c.Read(context.Background(), id, true)
			if calls != 3 {
				t.Fatal("manual refresh blocked by automatic interval", calls)
			}
		}
	}
}
