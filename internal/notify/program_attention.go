package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/gp-alex-chen/claude-session-manager/internal/agent"
	"github.com/gp-alex-chen/claude-session-manager/internal/state"
)

// AttentionState is the UI's canonical aggregate, not a second edge detector.
type AttentionState struct {
	Active      bool `json:"active"`
	Waiting     bool `json:"waiting"`
	UnreadCount int  `json:"unread_count"`
}

func attentionEvent(a AttentionState, force bool, reason string) ProgramEvent {
	e := newProgramEvent("attention_changed", "", agent.AgentInfo{})
	e.Attention, e.Force, e.Reason = &a, force, reason
	return e
}

func wantsAttention(c state.NotificationConfig) bool {
	if !c.Enabled {
		return false
	}
	for _, kind := range c.Events {
		if kind == "attention_changed" {
			return true
		}
	}
	return false
}

func (s *ProgramService) updatePendingLocked() {
	s.status.Pending = len(s.queue)
	if s.pendingAttention != nil {
		s.status.Pending++
	}
}

func (s *ProgramService) queueAttentionLocked(force bool, reason string) {
	if !s.attentionKnown || !wantsAttention(s.config) || s.closed || s.paused || s.ctx.Err() != nil {
		return
	}
	e := attentionEvent(s.attention, force, reason)
	// Replace pending snapshots, including a final off, independently of FIFO capacity.
	s.pendingAttention = &programJob{e, cloneProgramConfig(s.config), s.generation}
	s.updatePendingLocked()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *ProgramService) SetAttention(count int, waiting, force bool) {
	if count < 0 {
		count = 0
	}
	a := AttentionState{Active: count > 0 || waiting, Waiting: waiting, UnreadCount: count}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return
	}
	changed := !s.attentionKnown || a != s.attention
	s.attention, s.attentionKnown = a, true
	if changed || force {
		s.queueAttentionLocked(force, "state_changed")
	}
}

func (s *ProgramService) configure(c state.NotificationConfig) error {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.mu.Lock()
	if s.closed || reflect.DeepEqual(s.config, c) {
		s.mu.Unlock()
		return nil
	}
	previous := cloneProgramConfig(s.config)
	clearPrevious := s.attentionKnown && wantsAttention(previous) && (!wantsAttention(c) ||
		previous.Program != c.Program || previous.WorkingDir != c.WorkingDir || !reflect.DeepEqual(previous.Args, c.Args))
	s.generation++
	s.config = cloneProgramConfig(c)
	s.queue, s.pendingAttention, s.paused = nil, nil, true
	s.updatePendingLocked()
	if s.activeCancel != nil {
		s.activeCancel()
	}
	s.mu.Unlock()

	// The execution gate serializes cancellation/cleanup/new delivery. Neither
	// the state mutex nor App's lifecycle lock is held while invoking a program.
	s.runMu.Lock()
	var warning error
	if clearPrevious {
		if r := s.deliverClear(previous, "configuration_changed"); !r.Success {
			warning = fmt.Errorf("配置已保存，但旧通知程序清除失败：%s", r.Error)
		}
	}
	s.mu.Lock()
	s.paused = false
	s.queueAttentionLocked(true, "configuration_changed")
	s.mu.Unlock()
	s.runMu.Unlock()
	return warning
}

func (s *ProgramService) deliverClear(c state.NotificationConfig, reason string) ProgramResult {
	e := attentionEvent(AttentionState{}, true, reason)
	payload, _ := json.Marshal(e)
	duration := min(time.Duration(c.TimeoutSeconds)*time.Second, 3*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	s.mu.Lock()
	s.status.Running = true
	s.mu.Unlock()
	r := s.runner(ctx, c, append(payload, '\n'))
	r.EventID, r.Event, r.FinishedAt = e.EventID, e.Event, time.Now().UTC().Format(time.RFC3339Nano)
	s.mu.Lock()
	s.status.Running = false
	s.status.Last = &r
	s.mu.Unlock()
	if s.onResult != nil {
		s.onResult(r)
	}
	return r
}

func (s *ProgramService) closeWithAttention() {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.Stop()
	<-s.done
	s.mu.Lock()
	if s.cleanupFinished {
		s.mu.Unlock()
		return
	}
	s.cleanupFinished = true
	c := cloneProgramConfig(s.config)
	clear := wantsAttention(c)
	s.mu.Unlock()
	if clear {
		s.deliverClear(c, "shutdown")
	}
}
