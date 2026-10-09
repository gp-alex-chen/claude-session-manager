package provider

import (
	"context"
	"errors"

	"github.com/gp-alex-chen/claude-session-manager/internal/agent"
	"github.com/gp-alex-chen/claude-session-manager/internal/session"
	"github.com/gp-alex-chen/claude-session-manager/internal/usage"
)

// Claude delegates existing readers/watchers; those services retain ownership
// of their caches and lifecycle. This adapter never owns application state.
type ClaudeAdapter struct {
	History      func() ([]*session.Session, error)
	GetStatus    func(context.Context) []agent.AgentInfo
	GetUsage     func(string, string) usage.Summary
	RenameNative func(context.Context, string, string, string) error
	GetQuota     func(context.Context) (QuotaSnapshot, error)
}

func (p *ClaudeAdapter) AccountQuota(ctx context.Context) (QuotaSnapshot, error) {
	if p.GetQuota == nil {
		return QuotaSnapshot{Status: "unsupported", Message: "Claude 额度查询不可用"}, nil
	}
	return p.GetQuota(ctx)
}

func (p *ClaudeAdapter) Rename(ctx context.Context, id, name string) error {
	if err := p.ValidateSessionID(id); err != nil {
		return err
	}
	if !uuidPattern.MatchString(id) {
		return errors.New("Claude 原生重命名需要有效的会话 UUID")
	}
	if p.RenameNative == nil || p.History == nil {
		return errors.New("Claude 原生重命名不可用")
	}
	list, err := p.History()
	for _, s := range list {
		if s != nil && s.ID == id {
			return p.RenameNative(ctx, id, name, s.Dir)
		}
	}
	if err != nil {
		return err
	}
	return errors.New("未找到 Claude 会话，无法重命名")
}

func (p *ClaudeAdapter) Descriptor() Descriptor {
	return Descriptor{ID: Claude, Name: "Claude", NewSessionRestorable: true}
}
func (p *ClaudeAdapter) ValidateSessionID(id string) error { return session.ValidateID(id) }
func (p *ClaudeAdapter) PrepareLaunch(_ context.Context, req OpenRequest) (LaunchSpec, error) {
	spec := LaunchSpec{Command: "claude"}
	if req.Ref != nil {
		if req.Ref.Provider != Claude {
			return spec, errors.New("session belongs to another agent")
		}
		if err := p.ValidateSessionID(req.Ref.ID); err != nil {
			return spec, err
		}
		spec.Args = []string{"-r", req.Ref.ID}
		ref := *req.Ref
		spec.Identity = &ref
	}
	return spec, nil
}
func (p *ClaudeAdapter) ListSessions(context.Context) ([]Session, error) {
	var out []Session
	var err error
	if p.History != nil {
		var list []*session.Session
		list, err = p.History()
		for _, s := range list {
			out = append(out, Session{SessionRef{Claude, s.ID}, s.Dir, session.DisplayName(s), s.Time})
		}
	}
	return out, err
}
func (p *ClaudeAdapter) Status(ctx context.Context) ([]agent.AgentInfo, error) {
	if p.GetStatus == nil {
		return nil, errors.New("Claude status unavailable")
	}
	return p.GetStatus(ctx), nil
}
func (p *ClaudeAdapter) Usage(_ context.Context, id, dir string) (usage.Summary, error) {
	if p.GetUsage == nil {
		return usage.Summary{}, errors.New("Claude usage unavailable")
	}
	return p.GetUsage(id, dir), nil
}
