package provider

import (
	"context"
	"time"
)

const antigravityHistoryTimeout = 15 * time.Second

func (p *AntigravityAdapter) BeginService() { p.metadata.BeginService() }
func (p *AntigravityAdapter) CloseService() { p.metadata.CloseService() }

func (p *AntigravityAdapter) ListSessions(ctx context.Context) ([]Session, error) {
	return p.readHistory(ctx, false, "")
}

func (p *AntigravityAdapter) readHistory(parent context.Context, force bool, bin string) ([]Session, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, antigravityHistoryTimeout)
	defer cancel()
	return p.readHistoryWithin(ctx, force, bin)
}

// Ordinary callers bound queueing and lookup together. Exit supplies its own
// overall deadline so waiting behind a query does not consume the fresh ACP
// transport allowance; the transport timer begins only after owning the gate.
func (p *AntigravityAdapter) readHistoryWithin(ctx context.Context, force bool, bin string) ([]Session, error) {
	select {
	case p.historyGate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-p.historyGate }()
	epoch, _, serviceErr := p.metadata.state()
	if serviceErr != nil {
		return nil, serviceErr
	}
	now := p.now()
	if !force && p.historyEpoch == epoch && !p.historyAt.IsZero() && now.Sub(p.historyAt) < 10*time.Second {
		return append([]Session(nil), p.historyCache...), p.historyErr
	}
	source := p.config.History
	var list []Session
	var err error
	if source == nil {
		if bin == "" {
			bin, err = p.resolveExecutable()
		}
	}
	if err == nil {
		queryCtx, cancel := context.WithTimeout(ctx, antigravityHistoryTimeout)
		defer cancel()
		if source != nil {
			list, err = source.ListSessions(queryCtx)
		} else {
			list, err = p.metadata.ListSessions(queryCtx, bin)
		}
	}
	if current, _, serviceErr := p.metadata.state(); serviceErr != nil || current != epoch {
		if serviceErr != nil {
			return nil, serviceErr
		}
		return nil, errAntigravityACPStopped
	}
	p.historyEpoch = epoch
	if err == nil {
		p.historyCache = append([]Session(nil), list...)
	}
	p.historyErr = err
	// The TTL covers the returned result, not time spent querying and closing
	// ACP. A slow query must not make its fresh result immediately expire.
	p.historyAt = p.now()
	return append([]Session(nil), p.historyCache...), err
}
