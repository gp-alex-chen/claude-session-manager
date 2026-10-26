package app

import (
	"errors"

	"github.com/gp-alex-chen/claude-session-manager/internal/provider"
)

// ReferenceCodexSession grants only the explicit source->target reference.
// Reading and sending use native tools scoped to this runtime and identity.
func (a *App) ReferenceCodexSession(token, key string) (provider.SessionReference, error) {
	ref, err := provider.DecodeKey(key)
	if err != nil {
		return provider.SessionReference{}, err
	}
	if ref.Provider != provider.Codex {
		return provider.SessionReference{}, errors.New("目前只支持 Codex 会话之间引用")
	}
	if a.terms == nil || !a.terms.IsRunning(token) {
		return provider.SessionReference{}, errors.New("当前终端已关闭")
	}
	p, err := a.providers.Lookup(provider.Codex)
	if err != nil {
		return provider.SessionReference{}, err
	}
	live, ok := p.(*provider.CodexLive)
	if !ok {
		return provider.SessionReference{}, errors.New("当前 Codex 不支持会话引用")
	}
	return live.ReferenceSession(a.runtimeContext(), token, ref.ID)
}
