package notify

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestTaskbarAttentionOverridesAndRestoresCompletionCount(t *testing.T) {
	var calls []taskbarStatus
	b := &TaskbarBadge{changed: make(chan struct{}, 1), target: func() badgeTarget { return badgeTarget{1, 2} },
		apply: func(_ badgeTarget, status taskbarStatus) error { calls = append(calls, status); return nil }}
	for _, status := range []taskbarStatus{{count: 2}, {count: 2, attention: true}, {count: 4, attention: true}, {count: 4}, {attention: true}, {}} {
		if err := b.SetStatus(status.count, status.attention, false); err != nil {
			t.Fatal(err)
		}
		if got := calls[len(calls)-1]; got != status {
			t.Fatalf("got %+v, want %+v", got, status)
		}
	}
	before := len(calls)
	b.SetStatus(0, false, false)
	if len(calls) != before {
		t.Fatal("unchanged status was reapplied")
	}
	b.SetStatus(0, true, false)
	b.Close()
	if got := calls[len(calls)-1]; got != (taskbarStatus{}) {
		t.Fatal("close retained attention", got)
	}
}

func TestAttentionOnlyBadgeRetriesWindowAndExplorerRecovery(t *testing.T) {
	target := badgeTarget{}
	var calls []taskbarStatus
	b := &TaskbarBadge{changed: make(chan struct{}, 1), target: func() badgeTarget { return target },
		apply: func(_ badgeTarget, status taskbarStatus) error { calls = append(calls, status); return nil }}
	if err := b.SetStatus(0, true, false); err == nil {
		t.Fatal("missing window was accepted")
	}
	target = badgeTarget{1, 2}
	b.refresh()
	if len(calls) != 1 || !calls[0].attention {
		t.Fatal("attention-only was not retried", calls)
	}
	target.shell = 3
	b.refresh()
	b.refresh()
	b.refresh()
	if len(calls) != 3 {
		t.Fatal("attention-only shell recovery failed", calls)
	}
	b.Close()
}

func TestFailedAttentionSnapshotIsRetriedWithoutCachingFailure(t *testing.T) {
	fail := false
	b := &TaskbarBadge{changed: make(chan struct{}, 1), target: func() badgeTarget { return badgeTarget{1, 2} },
		apply: func(_ badgeTarget, _ taskbarStatus) error {
			if fail {
				return errors.New("fixture")
			}
			return nil
		}}
	if err := b.SetStatus(0, false, false); err != nil {
		t.Fatal(err)
	}
	fail = true
	if err := b.SetStatus(0, true, false); err == nil {
		t.Fatal("failure was hidden")
	}
	if b.lastAttention {
		t.Fatal("failed attention was cached")
	}
	fail = false
	b.refresh()
	if !b.lastAttention {
		t.Fatal("attention-only failure was not retried")
	}
	b.Close()
}

func TestTaskbarStatusSerializesAttentionAndCompletionUpdates(t *testing.T) {
	b := &TaskbarBadge{changed: make(chan struct{}, 1), target: func() badgeTarget { return badgeTarget{1, 2} },
		apply: func(_ badgeTarget, _ taskbarStatus) error { return nil }}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(count int) { defer wg.Done(); b.SetStatus(count, count%2 == 0, false); b.refresh() }(i)
	}
	wg.Wait()
	b.SetStatus(0, false, false)
	if b.lastCount != 0 || b.lastAttention {
		t.Fatal("latest combined snapshot was lost")
	}
	b.Close()
}

func TestTaskbarStatusDescriptionIncludesWaitingAndHiddenCompletionCount(t *testing.T) {
	status := taskbarStatus{count: 3, attention: true}
	if text := status.description(); !strings.Contains(text, "等待权限批准或用户输入") || !strings.Contains(text, "3 个会话已完成") {
		t.Fatal(text)
	}
	if text := (taskbarStatus{attention: true}).description(); !strings.Contains(text, "等待") {
		t.Fatal(text)
	}
	if text := (taskbarStatus{}).description(); text != "" {
		t.Fatal(text)
	}
}
