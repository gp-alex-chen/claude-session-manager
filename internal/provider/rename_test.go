package provider

import (
	"context"
	"errors"
	"github.com/gp-alex-chen/claude-session-manager/internal/session"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

const renameFixtureID = "01900000-0000-7000-8000-000000000001"

func TestCodexNativeRenameHandshakeAndParameters(t *testing.T) {
	f := &rpcFixture{Reader: strings.NewReader("{\"id\":1,\"result\":{}}\n{\"method\":\"thread/name/updated\",\"params\":{}}\n{\"id\":2,\"result\":{}}\n")}
	c := &CodexRPC{Open: func(context.Context) (io.ReadWriteCloser, error) { return f, nil }}
	name := `中文 "标题" C:\work & $(literal)`
	if err := c.Rename(context.Background(), renameFixtureID, name); err != nil {
		t.Fatal(err)
	}
	var methods []string
	for _, req := range f.requests {
		methods = append(methods, req["method"].(string))
	}
	if !reflect.DeepEqual(methods, []string{"initialize", "initialized", "thread/name/set"}) || !f.closed {
		t.Fatalf("requests=%v closed=%v", methods, f.closed)
	}
	params := f.requests[2]["params"].(map[string]any)
	if params["threadId"] != renameFixtureID || params["name"] != name {
		t.Fatalf("params=%v", params)
	}
}

func TestCodexNativeRenameRejectsErrorsAndInvalidInput(t *testing.T) {
	for _, response := range []string{`{"id":2,"error":{"message":"missing thread"}}`, `{"id":3,"result":{}}`, `{"id":2}`, `{"id":2,"result":null}`} {
		f := &rpcFixture{Reader: strings.NewReader("{\"id\":1,\"result\":{}}\n" + response + "\n")}
		c := &CodexRPC{Open: func(context.Context) (io.ReadWriteCloser, error) { return f, nil }}
		if err := c.Rename(context.Background(), renameFixtureID, "Title"); err == nil || !f.closed {
			t.Fatalf("accepted %s", response)
		}
	}
	c := &CodexRPC{Open: func(context.Context) (io.ReadWriteCloser, error) {
		t.Fatal("invalid input opened server")
		return nil, nil
	}}
	if err := c.Rename(context.Background(), "unsafe", "Title"); err == nil {
		t.Fatal("invalid ID")
	}
	for _, name := range []string{" ", "line\nbreak", strings.Repeat("字", 121)} {
		if err := c.Rename(context.Background(), renameFixtureID, name); err == nil {
			t.Fatalf("invalid title %q", name)
		}
	}
}

type renameHistory struct {
	sessions  []Session
	reads     int
	err       error
	renameErr error
	renamed   string
}

func (h *renameHistory) ListSessions(context.Context) ([]Session, error) {
	h.reads++
	return h.sessions, h.err
}
func (h *renameHistory) Rename(_ context.Context, id, name string) error {
	if h.renameErr != nil {
		return h.renameErr
	}
	h.renamed = id
	for i := range h.sessions {
		if h.sessions[i].Ref.ID == id {
			h.sessions[i].Name = name
		}
	}
	return nil
}

func TestCodexNativeRenameInvalidatesCacheOnlyAfterSuccess(t *testing.T) {
	h := &renameHistory{sessions: []Session{{Ref: SessionRef{Codex, renameFixtureID}, Name: "Old"}}}
	p := NewCodex(h)
	p.now = func() time.Time { return time.Unix(10, 0) }
	_, _ = p.ListSessions(context.Background())
	h.renameErr = errors.New("native denied")
	if err := p.Rename(context.Background(), renameFixtureID, "Rejected"); err == nil {
		t.Fatal("native failure hidden")
	}
	list, _ := p.ListSessions(context.Background())
	if list[0].Name != "Old" || h.reads != 1 {
		t.Fatalf("failure corrupted cache: %v %d", list, h.reads)
	}
	h.renameErr = nil
	if err := p.Rename(context.Background(), renameFixtureID, "New"); err != nil {
		t.Fatal(err)
	}
	list, _ = p.ListSessions(context.Background())
	if list[0].Name != "New" || h.reads != 2 || h.renamed != renameFixtureID {
		t.Fatalf("stale cache: %v %d", list, h.reads)
	}
}

func TestClaudeRenameMissingNodeIsActionable(t *testing.T) {
	err := RenameClaude(context.Background(), func(string) (string, error) { return "", errors.New("not found") }, renameFixtureID, "Title", "fixture")
	if err == nil || !strings.Contains(err.Error(), "Node.js 18") {
		t.Fatalf("error=%v", err)
	}
}

func TestClaudeRenameUsesKnownTargetEvenIfUnrelatedHistoryHasWarnings(t *testing.T) {
	called := false
	p := &ClaudeAdapter{
		History: func() ([]*session.Session, error) {
			return []*session.Session{{ID: renameFixtureID, Dir: "fixture"}}, errors.New("unrelated corrupt history")
		},
		RenameNative: func(_ context.Context, id, name, dir string) error {
			called = id == renameFixtureID && name == "Title" && dir == "fixture"
			return nil
		},
	}
	if err := p.Rename(context.Background(), renameFixtureID, "Title"); err != nil || !called {
		t.Fatalf("valid target blocked: called=%v err=%v", called, err)
	}
}
