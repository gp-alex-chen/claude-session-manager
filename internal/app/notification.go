package app

import (
	"context"
	"errors"

	"github.com/gp-alex-chen/claude-session-manager/internal/agent"
	"github.com/gp-alex-chen/claude-session-manager/internal/notify"
	"github.com/gp-alex-chen/claude-session-manager/internal/state"
)

func (a *App) GetNotificationConfig() (state.NotificationConfig, error) {
	return a.store.NotificationConfig()
}

func (a *App) SetNotificationConfig(c state.NotificationConfig) error {
	a.notificationMu.Lock()
	defer a.notificationMu.Unlock()
	c, err := state.NormalizeNotificationConfig(c)
	if err != nil {
		return err
	}
	if err := a.store.SaveNotificationConfig(c); err != nil {
		return err
	}
	a.lifecycleMu.Lock()
	service := a.notificationProgram
	a.lifecycleMu.Unlock()
	if service != nil {
		return service.Configure(c)
	}
	return nil
}

func (a *App) GetNotificationStatus() notify.ProgramStatus {
	a.lifecycleMu.Lock()
	service := a.notificationProgram
	a.lifecycleMu.Unlock()
	if service == nil {
		return notify.ProgramStatus{}
	}
	return service.Status()
}

// SetNotificationAttention includes all unread endings, including ordinary
// session exits, independently of the taskbar's successful-task-only count.
func (a *App) SetNotificationAttention(count int, waiting, force bool) {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if a.notificationProgram != nil {
		a.notificationProgram.SetAttention(count, waiting, force)
	}
}

func (a *App) TestNotification(event string) error {
	a.notificationMu.Lock()
	defer a.notificationMu.Unlock()
	a.lifecycleMu.Lock()
	service := a.notificationProgram
	a.lifecycleMu.Unlock()
	if service == nil {
		return errors.New("通知服务未启动")
	}
	return service.Test(event)
}

func (a *App) observeNotificationSource(ctx context.Context, source string, list []agent.AgentInfo) {
	a.lifecycleMu.Lock()
	service := a.notificationProgram
	current := a.ctx == ctx && ctx.Err() == nil
	a.lifecycleMu.Unlock()
	if current && service != nil {
		service.Observe(source, list)
	}
}
