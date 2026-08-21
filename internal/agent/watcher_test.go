package agent

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestStopCancelsFetcher(t *testing.T) {
	started := make(chan struct{})
	fetch := func(ctx context.Context) ([]AgentInfo, string) { close(started); <-ctx.Done(); return nil, "cancelled" }
	w := NewWatcherWithFetcher(fetch, nil)
	w.Start(context.Background())
	<-started
	done := make(chan struct{})
	go func() { w.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop waited for cancelled fetch")
	}
}

func TestStopIsIdempotentAndConcurrent(t *testing.T) {
	fetch := func(ctx context.Context) ([]AgentInfo, string) { <-ctx.Done(); return nil, "" }
	w := NewWatcherWithFetcher(fetch, nil)
	w.Start(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); w.Stop() }()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("concurrent Stop deadlocked")
	}
	w.Stop()
}

func TestStartStopStartCreatesFreshLifecycle(t *testing.T) {
	started := make(chan struct{}, 2)
	fetch := func(ctx context.Context) ([]AgentInfo, string) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ""
	}
	w := NewWatcherWithFetcher(fetch, nil)
	w.Start(context.Background())
	waitStarted(t, started)
	w.Stop()
	w.Start(context.Background())
	waitStarted(t, started)
	w.Stop()
}

func waitStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()
	deadline := time.After(time.Second)
	select {
	case <-started:
	case <-deadline:
		t.Fatal("fetch did not start")
	}
}

func TestPollSignatureEmitsOnlyOnChange(t *testing.T) {
	var emitted [][]AgentInfo
	w := NewWatcherWithFetcher(func(context.Context) ([]AgentInfo, string) { return nil, "" }, func(list []AgentInfo) { emitted = append(emitted, list) })
	first := []AgentInfo{{SessionID: "a", State: "working"}}
	w.fetch = func(context.Context) ([]AgentInfo, string) { return first, "raw" }
	fail := 0
	w.pollOnce(context.Background(), &fail)
	w.pollOnce(context.Background(), &fail)
	emitted[0][0].State = "emit-mutated"
	if w.Snapshot()[0].State != "working" {
		t.Fatal("emit slice mutation polluted cache")
	}
	first[0].State = "mutated"
	if w.Snapshot()[0].State != "working" {
		t.Fatal("fetch slice mutation polluted cache")
	}
	w.fetch = func(context.Context) ([]AgentInfo, string) {
		return []AgentInfo{{SessionID: "a", State: "done"}}, "raw2"
	}
	w.pollOnce(context.Background(), &fail)
	if len(emitted) != 2 {
		t.Fatalf("emissions=%d, want 2", len(emitted))
	}
	emitted[1][0].State = "emit-mutated-again"
	if w.Snapshot()[0].State != "done" {
		t.Fatal("second emit slice mutation polluted cache")
	}
}

func TestGetPerformsInitialSynchronousFetchAndReturnsCopy(t *testing.T) {
	calls := 0
	w := NewWatcherWithFetcher(func(context.Context) ([]AgentInfo, string) {
		calls++
		return []AgentInfo{{SessionID: "sync"}}, "raw"
	}, nil)
	got := w.Get()
	got[0].SessionID = "caller-mutated"
	if calls != 1 {
		t.Fatalf("fetch calls=%d, want 1", calls)
	}
	if snap := w.Snapshot(); snap[0].SessionID != "sync" {
		t.Fatal("Get result shares cache backing array")
	}
}
