package notify

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const WindowClassName = "ClaudeSessionManagerWindow"

type badgeTarget struct{ window, shell uintptr }

type taskbarStatus struct {
	count     int
	attention bool
}

func (s taskbarStatus) description() string {
	if s.attention {
		if s.count > 0 {
			return fmt.Sprintf("有会话等待权限批准或用户输入；另有 %d 个会话已完成，尚未查看", s.count)
		}
		return "有会话等待权限批准或用户输入"
	}
	if s.count > 0 {
		return fmt.Sprintf("%d 个会话已完成，尚未查看", s.count)
	}
	return ""
}

// TaskbarBadge owns only its last successful snapshot, never a COM object.
type TaskbarBadge struct {
	mu            sync.Mutex
	count         int
	attention     bool
	lastCount     int
	lastAttention bool
	lastTarget    badgeTarget
	valid, closed bool
	recovery      bool
	changed       chan struct{}
	target        func() badgeTarget
	apply         func(badgeTarget, taskbarStatus) error
}

func NewTaskbarBadge() *TaskbarBadge {
	return &TaskbarBadge{changed: make(chan struct{}, 1), target: findBadgeTarget, apply: applyTaskbarBadge}
}

func (b *TaskbarBadge) updateLocked(force bool) error {
	target := b.target()
	if target.window == 0 || target.shell == 0 {
		if b.count == 0 && !b.attention {
			return nil
		}
		return errors.New("任务栏或主窗口尚未就绪")
	}
	if b.valid && !force && b.count == b.lastCount && b.attention == b.lastAttention && target == b.lastTarget {
		return nil
	}
	if err := b.apply(target, taskbarStatus{count: b.count, attention: b.attention}); err != nil {
		return err
	}
	if b.valid && target.shell != b.lastTarget.shell {
		b.recovery = true
	}
	if b.count == 0 && !b.attention {
		b.recovery = false
	}
	b.valid, b.lastCount, b.lastAttention, b.lastTarget = true, b.count, b.attention, target
	return nil
}

func (b *TaskbarBadge) Set(count int, force bool) error {
	return b.SetStatus(count, false, force)
}

func (b *TaskbarBadge) SetStatus(count int, attention, force bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errors.New("任务栏角标已关闭")
	}
	if count < 0 {
		count = 0
	}
	b.count = count
	b.attention = attention
	select {
	case b.changed <- struct{}{}:
	default:
	}
	return b.updateLocked(force)
}

func (b *TaskbarBadge) refresh() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed && (b.count > 0 || b.attention) {
		recovering, shell := b.recovery, b.lastTarget.shell
		// Explorer may have published its Shell HWND before our taskbar button
		// exists. Repeat once on a later tick even after a successful HRESULT.
		if err := b.updateLocked(recovering); err == nil && recovering && shell == b.lastTarget.shell {
			b.recovery = false
		}
	}
}

// Only nonzero badges check Shell/window identity. No agent or account query
// is made, and an unchanged snapshot does not allocate an icon or call COM.
func (b *TaskbarBadge) Run(ctx context.Context) {
	var ticker *time.Ticker
	var ticks <-chan time.Time
	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
		b.Close()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.changed:
			b.mu.Lock()
			active := (b.count > 0 || b.attention) && !b.closed
			b.mu.Unlock()
			if active && ticker == nil {
				ticker = time.NewTicker(15 * time.Second)
				ticks = ticker.C
			}
			if !active && ticker != nil {
				ticker.Stop()
				ticker = nil
				ticks = nil
			}
		case <-ticks:
			b.refresh()
		}
	}
}

func (b *TaskbarBadge) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.count = 0
	b.attention = false
	_ = b.updateLocked(true)
	b.closed = true
}
