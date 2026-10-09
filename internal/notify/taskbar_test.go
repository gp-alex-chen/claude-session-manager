package notify

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestTaskbarBadgeCachesOnlySuccessfulApplicationsAndRecoversShell(t *testing.T) {
	target := badgeTarget{1, 2}
	var calls []int
	fail := false
	b := &TaskbarBadge{changed: make(chan struct{}, 1), target: func() badgeTarget { return target },
		apply: func(_ badgeTarget, count int) error {
			calls = append(calls, count)
			if fail {
				return errors.New("fixture")
			}
			return nil
		}}
	if err := b.Set(2, false); err != nil {
		t.Fatal(err)
	}
	b.Set(2, false)
	b.refresh()
	if len(calls) != 1 {
		t.Fatal("unchanged badge was reapplied", calls)
	}
	target.shell = 3
	b.refresh()
	if len(calls) != 2 {
		t.Fatal("new Shell did not reapply badge", calls)
	}
	b.refresh()
	if len(calls) != 3 {
		t.Fatal("new Shell was not given a delayed second application", calls)
	}
	b.refresh()
	if len(calls) != 3 {
		t.Fatal("recovery kept applying after the second success", calls)
	}
	fail = true
	if err := b.Set(1, false); err == nil {
		t.Fatal("failure was hidden")
	}
	if b.lastCount != 2 {
		t.Fatal("failed result was cached")
	}
	fail = false
	b.refresh()
	if b.lastCount != 1 {
		t.Fatal("failed application was not retried")
	}
	b.Set(0, false)
	b.refresh()
	if calls[len(calls)-1] != 0 {
		t.Fatal("zero did not clear badge")
	}
	count := len(calls)
	b.refresh()
	if len(calls) != count {
		t.Fatal("zero badge kept checking native state")
	}
	b.Close()
	b.Close()
	if err := b.Set(1, false); err == nil {
		t.Fatal("closed badge accepted updates")
	}
}

func TestTaskbarBadgeMissingWindowRetriesAndCancellationClears(t *testing.T) {
	target := badgeTarget{}
	var calls []int
	b := &TaskbarBadge{changed: make(chan struct{}, 1), target: func() badgeTarget { return target },
		apply: func(_ badgeTarget, count int) error { calls = append(calls, count); return nil }}
	if err := b.Set(1, false); err == nil {
		t.Fatal("missing window was accepted")
	}
	if b.valid {
		t.Fatal("missing window result was cached")
	}
	target = badgeTarget{1, 2}
	b.refresh()
	if len(calls) != 1 || calls[0] != 1 {
		t.Fatal(calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.Run(ctx)
	if !b.closed || calls[len(calls)-1] != 0 {
		t.Fatal("cancel did not clear and close", calls)
	}
}

func TestTaskbarBadgeSerializesConcurrentNativeApplications(t *testing.T) {
	b := &TaskbarBadge{changed: make(chan struct{}, 1), target: func() badgeTarget { return badgeTarget{1, 2} },
		apply: func(_ badgeTarget, _ int) error { return nil }}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(count int) { defer wg.Done(); b.Set(count, false); b.refresh() }(i)
	}
	wg.Wait()
	b.Set(0, false)
	if b.lastCount != 0 {
		t.Fatal("latest serialized update was not retained")
	}
	b.Close()
}
