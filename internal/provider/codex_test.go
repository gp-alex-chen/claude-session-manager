package provider

import (
	"context"
	"errors"
	"testing"
	"time"
)

type historyFunc func(context.Context) ([]Session, error)

func (f historyFunc) ListSessions(ctx context.Context) ([]Session, error) { return f(ctx) }

func TestCodexHistoryRefreshFailureRetainsSnapshotAndRecovers(t *testing.T) {
	clock := time.Unix(100, 0)
	failure := errors.New("CLI unavailable")
	read := 0
	p := NewCodex(historyFunc(func(context.Context) ([]Session, error) {
		read++
		if read == 2 {
			return nil, failure
		}
		return []Session{{Ref: SessionRef{Codex, "01900000-0000-7000-8000-000000000001"}, Name: "saved"}}, nil
	}))
	p.now = func() time.Time { return clock }
	first, err := p.ListSessions(context.Background())
	if err != nil || len(first) != 1 {
		t.Fatalf("first read: %v %v", first, err)
	}
	first[0].Name = "caller changed copy"
	clock = clock.Add(11 * time.Second)
	stale, err := p.ListSessions(context.Background())
	if !errors.Is(err, failure) || len(stale) != 1 || stale[0].Name != "saved" {
		t.Fatalf("failed refresh lost snapshot: %v %v", stale, err)
	}
	_, err = p.ListSessions(context.Background())
	if !errors.Is(err, failure) || read != 2 {
		t.Fatalf("failed refresh was not cached: %v, reads %d", err, read)
	}
	clock = clock.Add(11 * time.Second)
	if list, err := p.ListSessions(context.Background()); err != nil || len(list) != 1 || read != 3 {
		t.Fatalf("recovery: %v %v, reads %d", list, err, read)
	}
}
