package app

import (
	"context"
	"github.com/gp-alex-chen/claude-session-manager/internal/provider"
)

type antigravityRestoreSource interface {
	RestoreReferences(context.Context) (map[string]string, error)
	FilterRestoreReferences(map[string]string) map[string]string
}

func (a *App) antigravityRestoreReferences() map[string]string {
	p, err := a.providers.Lookup(provider.Antigravity)
	if err != nil {
		return nil
	}
	source, ok := p.(antigravityRestoreSource)
	if !ok {
		return nil
	}
	// Wails may cancel the UI context before invoking Shutdown. This exit
	// query owns a separate overall deadline inside the provider.
	refs, err := source.RestoreReferences(context.Background())
	if err != nil {
		a.log("保存 Antigravity 恢复引用失败: " + err.Error())
	}
	return refs
}
