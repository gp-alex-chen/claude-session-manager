package provider

import (
	"context"
	"errors"
	"time"
)

// StartupHistory is an in-process proof, never serialized to the frontend.
// Only a provider can construct its pinned, complete history snapshot.
type StartupHistory interface {
	PrepareLaunch(context.Context, OpenRequest) (LaunchSpec, error)
}
type StartupHistoryProvider interface {
	ReadStartupHistory(context.Context) ([]Session, StartupHistory, error)
}

const startupHistoryLifetime = 2 * time.Minute

type antigravityStartupHistory struct {
	owner    *AntigravityAdapter
	bin      string
	sessions []Session
	expires  time.Time
}

func (p *AntigravityAdapter) ReadStartupHistory(ctx context.Context) ([]Session, StartupHistory, error) {
	bin, err := p.resolveExecutable()
	if err != nil {
		return nil, nil, err
	}
	list, err := p.readHistory(ctx, true, bin)
	if err != nil {
		return nil, nil, err
	}
	snapshot := &antigravityStartupHistory{owner: p, bin: bin, sessions: append([]Session(nil), list...), expires: p.now().Add(startupHistoryLifetime)}
	return list, snapshot, nil
}
func (s *antigravityStartupHistory) PrepareLaunch(ctx context.Context, req OpenRequest) (LaunchSpec, error) {
	if s == nil || s.owner == nil {
		return LaunchSpec{}, errors.New("启动恢复快照无效")
	}
	return s.owner.prepareLaunch(ctx, req, s)
}
