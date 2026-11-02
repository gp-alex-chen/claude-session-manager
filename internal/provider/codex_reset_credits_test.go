package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func resetQuotaFixture(summary string) *rpcFixture {
	return &rpcFixture{Reader: strings.NewReader(`{"id":1,"result":{}}
{"id":2,"result":{"account":{"type":"chatgpt","email":"fixture@example.invalid","planType":"plus"}}}
{"id":3,"result":{"rateLimits":{"primary":{"usedPercent":80,"windowDurationMins":300}},"rateLimitResetCredits":` + summary + `}}
`)}
}

func TestCodexQuotaKeepsResetCreditCountAndOptionalDetails(t *testing.T) {
	for _, summary := range []string{`null`, `{"availableCount":0,"credits":[]}`, `{"availableCount":2,"credits":null}`, `{"availableCount":3,"credits":[{"id":"fixture-credit","resetType":"codexRateLimits","status":"available","grantedAt":100,"expiresAt":200,"title":"Fixture reset","description":"Fixture only"}]}`} {
		f := resetQuotaFixture(summary)
		rpc := &CodexRPC{Open: func(context.Context) (io.ReadWriteCloser, error) { return f, nil }}
		q, err := rpc.AccountQuota(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(q)
		var wire map[string]json.RawMessage
		json.Unmarshal(encoded, &wire)
		if summary == "null" {
			if string(wire["resetCredits"]) != "" && string(wire["resetCredits"]) != "null" {
				t.Fatal(string(encoded))
			}
			continue
		}
		if string(wire["resetCredits"]) != summary {
			t.Fatalf("lost credit metadata: %s", encoded)
		}
		if f.requests[3]["params"].(map[string]any)["excludeResetCreditDetails"] != true {
			t.Fatal("background poll fetched card details")
		}
	}
}

func TestQuotaCacheCopiesResetCreditDetails(t *testing.T) {
	f := resetQuotaFixture(`{"availableCount":2,"credits":[{"id":"fixture-credit","resetType":"codexRateLimits","status":"available","grantedAt":100,"expiresAt":200,"title":"Fixture reset","description":"Fixture only"}]}`)
	rpc := &CodexRPC{Open: func(context.Context) (io.ReadWriteCloser, error) { return f, nil }}
	cache := NewQuotaCache(func(ctx context.Context, _ ID) (QuotaSnapshot, error) { return rpc.AccountQuota(ctx) })
	q := cache.Read(context.Background(), Codex, false)
	q.ResetCredits.AvailableCount = 99
	q.ResetCredits.Credits[0].Title = "mutated"
	*q.ResetCredits.Credits[0].ExpiresAt = 999
	again := cache.Read(context.Background(), Codex, false)
	if again.ResetCredits.AvailableCount != 2 || again.ResetCredits.Credits[0].Title != "Fixture reset" || *again.ResetCredits.Credits[0].ExpiresAt != 200 {
		t.Fatal("caller mutated cached credit details", again)
	}
}

type resetCreditFixture struct {
	mu          sync.Mutex
	snapshot    QuotaSnapshot
	calls       int
	keys        []string
	ids         []string
	outcome     string
	refreshFail bool
}

func (f *resetCreditFixture) ReadResetCredits(context.Context) (QuotaSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneQuota(f.snapshot), nil
}
func (f *resetCreditFixture) RedeemResetCredit(_ context.Context, account, credit, key string, retry bool) (ResetCreditRedemption, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if account != f.snapshot.AccountKey {
		return ResetCreditRedemption{}, errors.New("account changed")
	}
	f.calls++
	f.keys = append(f.keys, key)
	f.ids = append(f.ids, credit)
	outcome := f.outcome
	if outcome == "" {
		outcome = "reset"
	}
	if outcome == "unknown" {
		f.snapshot.ResetCredits = &ResetCreditsSummary{AvailableCount: 0, Credits: []ResetCredit{}}
		return ResetCreditRedemption{Outcome: outcome}, nil
	}
	f.snapshot.ResetCredits = &ResetCreditsSummary{AvailableCount: 0, Credits: []ResetCredit{}}
	f.snapshot.Windows[0].UsedPercent = 0
	result := ResetCreditRedemption{Outcome: outcome}
	if !f.refreshFail {
		q := cloneQuota(f.snapshot)
		result.Snapshot = &q
	}
	return result, nil
}
func resetServiceFixture() (*ResetCreditService, *resetCreditFixture, *QuotaCache) {
	f := &resetCreditFixture{snapshot: QuotaSnapshot{Provider: Codex, Status: "available", AuthMode: "subscription", AccountKey: "fixture-account", Windows: []QuotaWindow{{UsedPercent: 90, WindowMinutes: 300}}, ResetCredits: &ResetCreditsSummary{AvailableCount: 1, Credits: []ResetCredit{{ID: "fixture-credit", ResetType: "codexRateLimits", Status: "available"}}}}}
	cache := NewQuotaCache(func(ctx context.Context, _ ID) (QuotaSnapshot, error) { return f.ReadResetCredits(ctx) })
	return NewResetCreditService(f, cache), f, cache
}
func TestResetCreditServiceDeduplicatesAndRefreshesWithoutQuotaThrottle(t *testing.T) {
	service, f, cache := resetServiceFixture()
	ctx := context.Background()
	cache.Read(ctx, Codex, false)
	view, err := service.Read(ctx)
	if err != nil || view.OfferToken == "" {
		t.Fatal(view, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := service.Consume(ctx, view.OfferToken, "fixture-credit")
			if e != nil || r.Outcome != "reset" {
				t.Error(r, e)
			}
		}()
	}
	wg.Wait()
	if f.calls != 1 {
		t.Fatal("duplicate consume", f.calls)
	}
	q := cache.Read(ctx, Codex, false)
	if q.Windows[0].UsedPercent != 0 || q.ResetCredits.AvailableCount != 0 {
		t.Fatal("stale cached quota", q)
	}
	if _, err = service.Consume(ctx, view.OfferToken, "other-credit"); err == nil {
		t.Fatal("changed selection accepted")
	}
}
func TestResetCreditUnknownKeepsAttemptWhenCardDisappearsAndDetailsReopen(t *testing.T) {
	service, f, _ := resetServiceFixture()
	ctx := context.Background()
	view, _ := service.Read(ctx)
	f.outcome = "unknown"
	first, err := service.Consume(ctx, view.OfferToken, "fixture-credit")
	if err != nil || first.Outcome != "unknown" {
		t.Fatal(first, err)
	}
	reopened, err := service.Read(ctx)
	if err != nil || !reopened.Pending || reopened.OfferToken != view.OfferToken || reopened.PendingCreditID != "fixture-credit" {
		t.Fatal(reopened, err)
	}
	f.outcome = "alreadyRedeemed"
	second, err := service.Consume(ctx, reopened.OfferToken, reopened.PendingCreditID)
	if err != nil || second.Outcome != "alreadyRedeemed" || f.keys[0] != f.keys[1] || f.ids[0] != f.ids[1] {
		t.Fatal(second, err, f.keys)
	}
}
func TestResetCreditSuccessSurvivesRefreshFailureAndNewLifecycleRejectsOldOffer(t *testing.T) {
	service, f, cache := resetServiceFixture()
	ctx := context.Background()
	view, _ := service.Read(ctx)
	f.refreshFail = true
	result, err := service.Consume(ctx, view.OfferToken, "fixture-credit")
	if err != nil || result.Outcome != "reset" || result.Snapshot != nil {
		t.Fatal(result, err)
	}
	again, err := service.Consume(ctx, view.OfferToken, "fixture-credit")
	if err != nil || again.Outcome != "reset" || f.calls != 1 {
		t.Fatal(again, err)
	}
	restarted := NewResetCreditService(f, cache)
	if _, err = restarted.Consume(ctx, view.OfferToken, "fixture-credit"); err == nil {
		t.Fatal("old offer accepted")
	}
}
func TestResetCreditOfferRejectsUnavailableUnknownExpiredAndChangedAccount(t *testing.T) {
	for _, mode := range []string{"unknown-type", "unknown-status", "expired", "account-changed", "missing-account"} {
		t.Run(mode, func(t *testing.T) {
			service, f, _ := resetServiceFixture()
			switch mode {
			case "unknown-type":
				f.snapshot.ResetCredits.Credits[0].ResetType = "unknown"
			case "unknown-status":
				f.snapshot.ResetCredits.Credits[0].Status = "redeeming"
			case "expired":
				expiry := time.Now().Add(-time.Hour).Unix()
				f.snapshot.ResetCredits.Credits[0].ExpiresAt = &expiry
			case "missing-account":
				f.snapshot.AccountKey = ""
			}
			view, _ := service.Read(context.Background())
			if mode == "account-changed" {
				f.snapshot.AccountKey = "other-account"
			}
			if _, err := service.Consume(context.Background(), view.OfferToken, "fixture-credit"); err == nil {
				t.Fatal("invalid offer accepted")
			}
			if f.calls != 0 {
				t.Fatal("consumed invalid offer")
			}
		})
	}
}

func resetRPCResponses(account string, count int, outcome string, postFail bool) string {
	summary := fmt.Sprintf(`{"availableCount":%d,"credits":[{"id":"fixture-credit","resetType":"codexRateLimits","status":"available","grantedAt":1}]}`, count)
	prefix := `{"id":1,"result":{}}
{"id":2,"result":{"account":{"type":"chatgpt","planType":"plus"}}}
` + fmt.Sprintf(`{"id":3,"result":{"accountId":%q,"rateLimits":{"primary":{"usedPercent":90,"windowDurationMins":300}},"rateLimitResetCredits":%s}}`, account, summary) + "\n"
	if outcome == "" {
		return prefix
	}
	prefix += fmt.Sprintf(`{"id":4,"result":{"outcome":%q}}`, outcome) + "\n"
	if postFail {
		return prefix + `{"id":5,"error":{"message":"fixture refresh failure"}}` + "\n"
	}
	return prefix + `{"id":5,"result":{"account":{"type":"chatgpt","planType":"plus"}}}
` + fmt.Sprintf(`{"id":6,"result":{"accountId":%q,"rateLimits":{"primary":{"usedPercent":0,"windowDurationMins":300}},"rateLimitResetCredits":{"availableCount":0,"credits":[]}}}`, account) + "\n"
}
func TestCodexResetRPCUsesOfficialMethodsAndKeepsOutcomeSeparateFromRefresh(t *testing.T) {
	for _, outcome := range []string{"reset", "alreadyRedeemed", "nothingToReset", "noCredit", "future-outcome"} {
		t.Run(outcome, func(t *testing.T) {
			f := &rpcFixture{Reader: strings.NewReader(resetRPCResponses("fixture-account", 1, outcome, false))}
			rpc := &CodexRPC{Open: func(context.Context) (io.ReadWriteCloser, error) { return f, nil }}
			// Read only the fixture account identity, then use a new fixture connection.
			identity := &rpcFixture{Reader: strings.NewReader(resetRPCResponses("fixture-account", 1, "", false))}
			idRPC := &CodexRPC{Open: func(context.Context) (io.ReadWriteCloser, error) { return identity, nil }}
			q, err := idRPC.ReadResetCredits(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			r, err := rpc.RedeemResetCredit(context.Background(), q.AccountKey, "fixture-credit", "fixture-key", false)
			if err != nil {
				t.Fatal(err)
			}
			want := outcome
			if outcome == "future-outcome" {
				want = "unknown"
			}
			if r.Outcome != want {
				t.Fatal(r)
			}
			request := f.requests[4]
			params := request["params"].(map[string]any)
			if request["method"] != "account/rateLimitResetCredit/consume" || params["creditId"] != "fixture-credit" || params["idempotencyKey"] != "fixture-key" {
				t.Fatal(request)
			}
			if f.requests[3]["params"].(map[string]any)["excludeResetCreditDetails"] != false {
				t.Fatal("details omitted")
			}
			if outcome != "future-outcome" && (r.Snapshot == nil || r.Snapshot.Windows[0].UsedPercent != 0) {
				t.Fatal("did not refresh", r)
			}
			for _, request := range f.requests {
				m := request["method"].(string)
				if strings.HasPrefix(m, "thread/") || strings.HasPrefix(m, "turn/") {
					t.Fatal("model called")
				}
			}
		})
	}
}
func TestCodexResetRPCKnownSuccessDoesNotBecomeUnknownOnRefreshFailure(t *testing.T) {
	id := &rpcFixture{Reader: strings.NewReader(resetRPCResponses("fixture-account", 1, "", false))}
	rpc := &CodexRPC{Open: func(context.Context) (io.ReadWriteCloser, error) { return id, nil }}
	q, _ := rpc.ReadResetCredits(context.Background())
	f := &rpcFixture{Reader: strings.NewReader(resetRPCResponses("fixture-account", 1, "reset", true))}
	rpc.Open = func(context.Context) (io.ReadWriteCloser, error) { return f, nil }
	r, err := rpc.RedeemResetCredit(context.Background(), q.AccountKey, "fixture-credit", "fixture-key", false)
	if err != nil || r.Outcome != "reset" || r.Snapshot != nil || !strings.Contains(r.Message, "读取失败") {
		t.Fatal(r, err)
	}
}
func TestCodexResetRPCRefusesChangedAccountAndRetriesMissingCardWithSameKey(t *testing.T) {
	f := &rpcFixture{Reader: strings.NewReader(resetRPCResponses("fixture-account", 1, "", false))}
	rpc := &CodexRPC{Open: func(context.Context) (io.ReadWriteCloser, error) { return f, nil }}
	q, _ := rpc.ReadResetCredits(context.Background())
	changed := &rpcFixture{Reader: strings.NewReader(resetRPCResponses("other-account", 1, "", false))}
	rpc.Open = func(context.Context) (io.ReadWriteCloser, error) { return changed, nil }
	if _, err := rpc.RedeemResetCredit(context.Background(), q.AccountKey, "fixture-credit", "fixture-key", false); err == nil || len(changed.requests) != 4 {
		t.Fatal("changed account consumed", changed.requests, err)
	}
	gone := &rpcFixture{Reader: strings.NewReader(resetRPCResponses("fixture-account", 0, "alreadyRedeemed", false))}
	rpc.Open = func(context.Context) (io.ReadWriteCloser, error) { return gone, nil }
	result, err := rpc.RedeemResetCredit(context.Background(), q.AccountKey, "fixture-credit", "fixture-key", true)
	if err != nil || result.Outcome != "alreadyRedeemed" || gone.requests[4]["params"].(map[string]any)["idempotencyKey"] != "fixture-key" {
		t.Fatal(result, err)
	}
}
func TestQuotaMutationSerializesAgainstOldRead(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	cache := NewQuotaCache(func(context.Context, ID) (QuotaSnapshot, error) {
		close(entered)
		<-release
		return QuotaSnapshot{Status: "available", Windows: []QuotaWindow{{UsedPercent: 99}}}, nil
	})
	oldDone := make(chan struct{})
	go func() { cache.Read(context.Background(), Codex, false); close(oldDone) }()
	<-entered
	mutationDone := make(chan struct{})
	go func() {
		cache.Refresh(context.Background(), Codex, func(context.Context) (QuotaSnapshot, error) {
			return QuotaSnapshot{Status: "available", Windows: []QuotaWindow{{UsedPercent: 0}}}, nil
		})
		close(mutationDone)
	}()
	close(release)
	<-oldDone
	<-mutationDone
	if q := cache.Read(context.Background(), Codex, false); q.Windows[0].UsedPercent != 0 {
		t.Fatal("old read overwrote mutation", q)
	}
}

func TestCompletedResetReplayReadsCurrentQuotaInsteadOfReturningOldAccountSnapshot(t *testing.T) {
	service, f, _ := resetServiceFixture()
	ctx := context.Background()
	view, _ := service.Read(ctx)
	if _, err := service.Consume(ctx, view.OfferToken, "fixture-credit"); err != nil {
		t.Fatal(err)
	}
	f.snapshot.Windows[0].UsedPercent = 23
	current, err := service.Consume(ctx, view.OfferToken, "fixture-credit")
	if err != nil || current.Snapshot == nil || current.Snapshot.Windows[0].UsedPercent != 23 || f.calls != 1 {
		t.Fatal("replayed stale snapshot", current, err)
	}
	f.snapshot.AccountKey = "other-account"
	changed, err := service.Consume(ctx, view.OfferToken, "fixture-credit")
	if err != nil || changed.Outcome != "reset" || changed.Snapshot != nil || f.calls != 1 {
		t.Fatal("replayed previous account quota", changed, err)
	}
}
func TestResetSummaryMissingCountIsNotZero(t *testing.T) {
	for _, input := range []string{`{}`, `{"credits":[]}`, `{"availableCount":-1}`} {
		f := resetQuotaFixture(input)
		rpc := &CodexRPC{Open: func(context.Context) (io.ReadWriteCloser, error) { return f, nil }}
		if _, err := rpc.AccountQuota(context.Background()); err == nil {
			t.Fatal("invalid count accepted", input)
		}
	}
}
