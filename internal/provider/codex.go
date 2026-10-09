package provider

import (
	"context"
	"errors"
	"sync"
	"time"
)

type CodexHistory interface {
	ListSessions(context.Context) ([]Session, error)
}

// CodexAdapter uses the CLI's public app-server API for metadata, never parses
// Codex's private transcript/database format. Mutations use public APIs too.
type CodexAdapter struct {
	History  CodexHistory
	mu       sync.Mutex
	cached   []Session
	lastErr  error
	lastRead time.Time
	now      func() time.Time
}

func NewCodex(history CodexHistory) *CodexAdapter {
	return &CodexAdapter{History: history, now: time.Now}
}
func (p *CodexAdapter) Descriptor() Descriptor            { return Descriptor{ID: Codex, Name: "Codex"} }
func (p *CodexAdapter) ValidateSessionID(id string) error { return validateRef(SessionRef{Codex, id}) }
func (p *CodexAdapter) PrepareLaunch(_ context.Context, req OpenRequest) (LaunchSpec, error) {
	spec := LaunchSpec{Command: "codex", Args: []string{"--no-daemon"}}
	if req.Ref != nil {
		if req.Ref.Provider != Codex {
			return spec, errors.New("session belongs to another agent")
		}
		if err := p.ValidateSessionID(req.Ref.ID); err != nil {
			return spec, err
		}
		spec.Args = []string{"resume", req.Ref.ID, "--no-daemon"}
		ref := *req.Ref
		spec.Identity = &ref
	}
	return spec, nil
}
func (p *CodexAdapter) ListSessions(ctx context.Context) ([]Session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	if !p.lastRead.IsZero() && now.Sub(p.lastRead) < 10*time.Second {
		return append([]Session(nil), p.cached...), p.lastErr
	}
	if p.History == nil {
		return nil, nil
	}
	list, err := p.History.ListSessions(ctx)
	if err == nil {
		p.cached = append([]Session(nil), list...)
	}
	p.lastRead = now
	p.lastErr = err
	return append([]Session(nil), p.cached...), err
}

func (p *CodexAdapter) invalidateHistory() {
	p.mu.Lock()
	p.lastRead = time.Time{}
	p.mu.Unlock()
}

func (p *CodexAdapter) Rename(ctx context.Context, id, name string) error {
	if err := p.ValidateSessionID(id); err != nil {
		return err
	}
	renamer, ok := p.History.(RenameProvider)
	if !ok {
		return errors.New("Codex 原生重命名不可用")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := renamer.Rename(ctx, id, name); err != nil {
		return err
	}
	for i := range p.cached {
		if p.cached[i].Ref.ID == id {
			p.cached[i].Name = name
		}
	}
	// Read through immediately; retain the successful native title if listing fails.
	p.lastRead = time.Time{}
	p.lastErr = nil
	return nil
}
