package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAntigravityQuotaCallbackWhitelistAndLatestWholeSnapshot(t *testing.T) {
	p := NewAntigravity(AntigravityConfig{})
	registry := NewRegistry()
	if err := registry.Register(p); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	write := func(token, payload string) {
		t.Helper()
		path := filepath.Join(root, token+".json")
		if err := WriteAntigravitySnapshot(strings.NewReader(payload), path); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), "PRIVATE") {
			t.Fatal("private metadata persisted", string(data))
		}
		p.runtimes[token] = &antigravityRuntime{snapshotPath: path}
	}
	write("first", `{"agent_state":"idle","email":"PRIVATE","plan_tier":"Pro","quota":{"gemini-weekly":{"remaining_fraction":0,"reset_in_seconds":60,"secret":"PRIVATE"},"amount-only":{"remaining_amount":50},"disabled":{"remaining_fraction":1,"disabled":true},"bad":{"remaining_fraction":2}}}`)
	q, err := registry.AccountQuota(context.Background(), Antigravity)
	if err != nil || q.Status != "available" || len(q.Windows) != 1 || q.Windows[0].UsedPercent != 100 || q.ReadAt.IsZero() || q.Plan != "Pro" {
		t.Fatal("callback quota not preserved", q, err)
	}
	if q.Windows[0].ResetsAt != q.ReadAt.Unix()+60 {
		t.Fatal("relative reset not based on collection time", q)
	}
	firstTime := q.ReadAt
	// Use an explicit later collection time without sleeping or real accounts.
	write("second", `{"plan_tier":"Ultra","quota":{"other-group":{"remaining_fraction":1,"reset_time":"2027-01-01T00:00:00Z"}}}`)
	path := p.runtimes["second"].snapshotPath
	data, _ := os.ReadFile(path)
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["quota_observed_at"] = firstTime.Add(time.Second).UnixMilli()
	data, _ = json.Marshal(doc)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	q, err = registry.AccountQuota(context.Background(), Antigravity)
	if err != nil || q.Plan != "Ultra" || len(q.Windows) != 1 || q.Windows[0].ID != "other-group" || q.Windows[0].UsedPercent != 0 {
		t.Fatal("terminals were merged or latest callback was ignored", q, err)
	}
	p.now = func() time.Time { return firstTime.Add(time.Hour) }
	q, _ = registry.AccountQuota(context.Background(), Antigravity)
	if !q.Stale || !q.ReadAt.Equal(firstTime.Add(time.Second)) {
		t.Fatal("old callback shown as fresh", q)
	}
	delete(p.runtimes, "first")
	delete(p.runtimes, "second")
	q, _ = registry.AccountQuota(context.Background(), Antigravity)
	if q.Status == "available" || len(q.Windows) != 0 {
		t.Fatal("closed terminals retained current quota", q)
	}
}

func TestAntigravityMalformedQuotaDoesNotBreakTaskStatus(t *testing.T) {
	for _, raw := range []string{`false`, `[]`, `{"broken":{"remaining_fraction":"bad"}}`, `{"amount":{"remaining_amount":42}}`} {
		path := filepath.Join(t.TempDir(), "snapshot.json")
		if err := WriteAntigravitySnapshot(strings.NewReader(`{"agent_state":"working","quota":`+raw+`}`), path); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(path)
		if !strings.Contains(string(data), `"agent_state":"working"`) {
			t.Fatal("bad quota lost execution state", string(data))
		}
	}
}

func TestAntigravityQuotaRevisionOnlyTracksQuotaObservations(t *testing.T) {
	p := NewAntigravity(AntigravityConfig{})
	path := filepath.Join(t.TempDir(), "snapshot.json")
	p.runtimes["runtime"] = &antigravityRuntime{snapshotPath: path}
	status := func(data string) uint64 {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Status(context.Background()); err != nil {
			t.Fatal(err)
		}
		return p.QuotaRevision()
	}
	if got := status(`{"agent_state":"working","quota_observed_at":1000}`); got != 0 {
		t.Fatal("task-only callback advanced quota", got)
	}
	const quota = `"quota_observed_at":1000,"quota":{"gemini-5h":{"remaining_fraction":0.25}}`
	if got := status(`{"agent_state":"working",` + quota + `}`); got != 1 {
		t.Fatal("new quota was not observed", got)
	}
	if got := status(`{"agent_state":"idle",` + quota + `}`); got != 1 {
		t.Fatal("task state changed without a quota change", got)
	}
	if got := status(`{"agent_state":"idle","quota_observed_at":2000}`); got != 2 {
		t.Fatal("missing quota did not invalidate the earlier observation", got)
	}
}

func TestAntigravityQuotaKeepsModelGroupsAndWindowDurations(t *testing.T) {
	q := antigravityQuota(antigravitySnapshot{QuotaObservedAt: 1, Quota: json.RawMessage(`{
		"gemini-5h":{"remaining_fraction":0.8},"gemini-weekly":{"remaining_fraction":0.6},
		"3p-5h":{"remaining_fraction":0.4},"3p-weekly":{"remaining_fraction":0},
		"future-unknown":{"remaining_fraction":1},"Gemini":{"remaining_fraction":0.5}}`)})
	windows := map[string]QuotaWindow{}
	for _, w := range q.Windows {
		windows[w.ID] = w
	}
	for _, group := range []struct{ prefix, scope string }{{"gemini", "Gemini"}, {"3p", "第三方"}} {
		short, weekly := windows[group.prefix+"-5h"], windows[group.prefix+"-weekly"]
		if short.Scope != group.scope || weekly.Scope != group.scope || short.WindowMinutes != 300 || weekly.WindowMinutes != 10080 {
			t.Fatalf("lost native group/window identity: %#v %#v", short, weekly)
		}
	}
	if unknown := windows["future-unknown"]; unknown.Scope != "bucket:"+unknown.ID || unknown.WindowMinutes != 0 {
		t.Fatal("unknown bucket guessed into another group", unknown)
	}
	if windows["Gemini"].Scope == windows["gemini-5h"].Scope {
		t.Fatal("unknown bucket collided with a known group")
	}
	q = antigravityQuota(antigravitySnapshot{QuotaObservedAt: 1, Quota: json.RawMessage(`{"gemini-weekly":{"remaining_fraction":0}}`)})
	if len(q.Windows) != 1 || q.Windows[0].Scope != "Gemini" || q.Windows[0].UsedPercent != 100 {
		t.Fatal("absent groups/windows retained or zero discarded", q)
	}
}
