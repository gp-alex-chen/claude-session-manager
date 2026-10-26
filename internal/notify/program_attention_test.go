package notify

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/gp-alex-chen/claude-session-manager/internal/state"
)

func attentionConfig() state.NotificationConfig {
	c := state.DefaultNotificationConfig()
	c.Enabled, c.Program, c.Events = true, "fixture.exe", []string{"attention_changed"}
	return c
}

func TestAttentionSnapshotsCoalesceWithoutLosingFinalClear(t *testing.T) {
	started := make(chan ProgramEvent, 40)
	release := make(chan struct{})
	results := make(chan ProgramResult, 40)
	var first atomic.Bool
	s := newProgramService(context.Background(), attentionConfig(), func(ctx context.Context, _ state.NotificationConfig, b []byte) ProgramResult {
		var e ProgramEvent
		if err := json.Unmarshal(b, &e); err != nil {
			t.Error(err)
		}
		started <- e
		if first.CompareAndSwap(false, true) {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return ProgramResult{Success: true}
	}, func(r ProgramResult) { results <- r })
	defer s.Close()
	s.SetAttention(0, true, false)
	initial := <-started
	if initial.Attention == nil || !initial.Attention.Active || !initial.Attention.Waiting {
		t.Fatalf("waiting snapshot: %#v", initial)
	}
	for i := 0; i < programQueueLimit; i++ {
		if err := s.Test("task_completed"); err != nil {
			t.Fatal(err)
		}
	}
	s.SetAttention(2, true, false)
	s.SetAttention(2, false, false)
	s.SetAttention(0, false, false)
	if got := s.Status().Pending; got != programQueueLimit+1 {
		t.Fatalf("pending = %d", got)
	}
	close(release)
	receiveResult(t, results)
	clear := <-started
	if clear.Event != "attention_changed" || clear.Attention == nil || clear.Attention.Active || clear.Attention.UnreadCount != 0 || clear.Attention.Waiting {
		t.Fatalf("latest clear lost: %#v", clear)
	}
	receiveResult(t, results)
	for i := 0; i < programQueueLimit; i++ {
		<-started
		receiveResult(t, results)
	}
	s.SetAttention(0, false, false)
	if s.Status().Pending != 0 {
		t.Fatal("duplicate state queued")
	}
}

func TestAttentionReconfigureClearsOldTargetAndSynchronizesNewTarget(t *testing.T) {
	jobs := make(chan struct {
		config state.NotificationConfig
		event  ProgramEvent
	}, 16)
	results := make(chan ProgramResult, 16)
	c := attentionConfig()
	s := newProgramService(context.Background(), c, func(_ context.Context, config state.NotificationConfig, b []byte) ProgramResult {
		var e ProgramEvent
		_ = json.Unmarshal(b, &e)
		jobs <- struct {
			config state.NotificationConfig
			event  ProgramEvent
		}{config, e}
		return ProgramResult{Success: true}
	}, func(r ProgramResult) { results <- r })
	defer s.Close()
	s.SetAttention(2, false, false)
	receiveResult(t, results)
	<-jobs
	next := c
	next.Program = "next.exe"
	s.Configure(next)
	oldClear := <-jobs
	if oldClear.config.Program != c.Program || oldClear.event.Attention.Active {
		t.Fatalf("old target retained attention: %#v", oldClear)
	}
	receiveResult(t, results)
	receiveResult(t, results)
	current := <-jobs
	if current.config.Program != next.Program || !current.event.Attention.Active || current.event.Attention.UnreadCount != 2 {
		t.Fatalf("new target not synchronized: %#v", current)
	}
	next.Enabled = false
	s.Configure(next)
	receiveResult(t, results)
	if e := <-jobs; e.event.Attention.Active {
		t.Fatal("disable did not clear")
	}
	s.SetAttention(0, true, false)
	if s.Status().Pending != 0 {
		t.Fatal("disabled state queued")
	}
	next.Enabled = true
	s.Configure(next)
	receiveResult(t, results)
	if e := <-jobs; !e.event.Attention.Waiting {
		t.Fatal("enable missed current state")
	}
}

func TestAttentionShutdownClearsAfterCancelledRunEvenIfParentCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan ProgramEvent, 4)
	var calls atomic.Int32
	s := newProgramService(ctx, attentionConfig(), func(ctx context.Context, _ state.NotificationConfig, b []byte) ProgramResult {
		var e ProgramEvent
		_ = json.Unmarshal(b, &e)
		started <- e
		if calls.Add(1) == 1 {
			<-ctx.Done()
		} else if ctx.Err() != nil {
			t.Error("cleanup inherited cancelled context")
		}
		return ProgramResult{Success: true}
	}, nil)
	s.SetAttention(1, false, false)
	<-started
	cancel()
	s.Close()
	clear := <-started
	if clear.Attention.Active || clear.Test {
		t.Fatalf("shutdown clear: %#v", clear)
	}
	s.Close()
	if calls.Load() != 2 {
		t.Fatal("cleanup repeated")
	}
}

func TestAttentionLegacyDoesNotInvokeAndUnknownShutdownOnlyClears(t *testing.T) {
	c := state.DefaultNotificationConfig()
	c.Enabled = true
	c.Program = "fixture.exe"
	var calls atomic.Int32
	s := newProgramService(context.Background(), c, func(context.Context, state.NotificationConfig, []byte) ProgramResult {
		calls.Add(1)
		return ProgramResult{Success: true}
	}, nil)
	s.SetAttention(1, true, false)
	s.Close()
	if calls.Load() != 0 {
		t.Fatal("legacy subscription received aggregate events")
	}
	s = newProgramService(context.Background(), attentionConfig(), func(context.Context, state.NotificationConfig, []byte) ProgramResult {
		calls.Add(1)
		return ProgramResult{Success: true}
	}, nil)
	s.Close()
	if calls.Load() != 1 {
		t.Fatal("shutdown before first UI snapshot did not clear subscribed output")
	}
}

func TestAttentionForceAndFullSnapshotChangesAreDelivered(t *testing.T) {
	jobs := make(chan ProgramEvent, 12)
	results := make(chan ProgramResult, 12)
	s := newProgramService(context.Background(), attentionConfig(), func(_ context.Context, _ state.NotificationConfig, b []byte) ProgramResult {
		var e ProgramEvent
		_ = json.Unmarshal(b, &e)
		jobs <- e
		return ProgramResult{Success: true}
	}, func(r ProgramResult) { results <- r })
	defer s.Close()
	for _, change := range []struct {
		count          int
		waiting, force bool
	}{
		{-1, false, false}, {1, false, false}, {2, false, false}, {0, true, false}, {0, true, true},
	} {
		s.SetAttention(change.count, change.waiting, change.force)
		receiveResult(t, results)
		e := <-jobs
		if e.Attention.UnreadCount != max(change.count, 0) || e.Attention.Waiting != change.waiting || e.Force != change.force || e.Test {
			t.Fatalf("wrong snapshot: %#v", e)
		}
	}
	if err := s.Test("attention_changed"); err != nil {
		t.Fatal(err)
	}
	receiveResult(t, results)
	if e := <-jobs; !e.Test || !e.Attention.Waiting {
		t.Fatalf("test snapshot: %#v", e)
	}
}

func TestAttentionReconfigureCancelsStaleDeliveryAndClearFailureStillDisables(t *testing.T) {
	started := make(chan ProgramEvent, 8)
	results := make(chan ProgramResult, 8)
	var calls atomic.Int32
	s := newProgramService(context.Background(), attentionConfig(), func(ctx context.Context, _ state.NotificationConfig, b []byte) ProgramResult {
		var e ProgramEvent
		_ = json.Unmarshal(b, &e)
		started <- e
		if calls.Add(1) == 1 {
			<-ctx.Done()
			return ProgramResult{Error: "cancelled"}
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("cleanup not bounded")
		}
		return ProgramResult{Error: "fixture cleanup failed"}
	}, func(r ProgramResult) { results <- r })
	s.SetAttention(1, false, false)
	<-started
	s.SetAttention(2, false, false)
	disabled := attentionConfig()
	disabled.Enabled = false
	if err := s.Configure(disabled); err == nil {
		t.Fatal("cleanup warning not reported")
	}
	receiveResult(t, results)
	receiveResult(t, results)
	if e := <-started; e.Attention.Active || e.Reason != "configuration_changed" {
		t.Fatalf("stale on survived: %#v", e)
	}
	s.SetAttention(3, true, true)
	s.Close()
	if calls.Load() != 2 || s.Status().Last.Success {
		t.Fatal("failed cleanup retried or disabled config executed")
	}
}
