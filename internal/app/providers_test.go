package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gp-alex-chen/claude-session-manager/internal/provider"
	"github.com/gp-alex-chen/claude-session-manager/internal/terminal"
)

type lifecycleCodex struct {
	*provider.CodexAdapter
	mu            sync.Mutex
	begin, closed int
	onClose       func()
}

func (p *lifecycleCodex) BeginService() { p.mu.Lock(); p.begin++; p.mu.Unlock() }
func (p *lifecycleCodex) CloseService() {
	p.mu.Lock()
	p.closed++
	p.mu.Unlock()
	if p.onClose != nil {
		p.onClose()
	}
}

func TestSharedProviderShutdownPersistStartupRestore(t *testing.T) {
	a, store, _, dir := testApp(t)
	p := &lifecycleCodex{CodexAdapter: provider.NewCodex(fakeCodexHistory{})}
	a.providers = a.newProviderRegistry(false)
	a.providers.Register(p)
	a.lookPath = func(name string) (string, error) { return name + ".exe", nil }
	a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(string, string, int, int, []string) (terminal.Pty, error) { return newShutdownPty(), nil })
	key := "codex:01900000-0000-7000-8000-000000000001"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.Startup(ctx)
	if p.begin != 1 {
		t.Fatalf("service lifecycle not begun: %d", p.begin)
	}
	if _, err := a.StartSession(key, dir); err != nil {
		t.Fatal(err)
	}
	p.onClose = func() {
		if a.terms.IsRunning(key) {
			t.Error("server closed before terminal snapshot/close")
		}
		if got := a.GetOpenSessions(); !reflect.DeepEqual(got, []string{key}) {
			t.Errorf("identity not persisted: %v", got)
		}
	}
	a.Shutdown(context.Background())
	if p.closed != 1 {
		t.Fatalf("service not closed: %d", p.closed)
	}
	a.Startup(ctx)
	if p.begin != 2 {
		t.Fatal("service lifecycle not restarted")
	}
	if _, err := a.StartSession(a.GetOpenSessions()[0], dir); err != nil {
		t.Fatal(err)
	}
	a.Shutdown(context.Background())
	if p.closed != 2 {
		t.Fatal("restored lifecycle not closed")
	}
}

func TestSharedProviderUpdateClosesAfterPersistenceAndReopensOnFailure(t *testing.T) {
	a, store, _, dir := testApp(t)
	p := &lifecycleCodex{CodexAdapter: provider.NewCodex(fakeCodexHistory{})}
	a.providers = a.newProviderRegistry(false)
	a.providers.Register(p)
	a.lookPath = func(name string) (string, error) { return name + ".exe", nil }
	a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(string, string, int, int, []string) (terminal.Pty, error) { return newShutdownPty(), nil })
	key := "codex:01900000-0000-7000-8000-000000000001"
	if _, err := a.StartSession(key, dir); err != nil {
		t.Fatal(err)
	}
	p.onClose = func() {
		if a.terms.IsRunning(key) {
			t.Error("service closed before PTY")
		}
		if got := a.GetOpenSessions(); !reflect.DeepEqual(got, []string{key}) {
			t.Errorf("update did not persist identity: %v", got)
		}
	}
	failure := errors.New("synthetic replacement failure")
	err := a.applyDownloadedUpdate("fixture.exe", func(string, bool) error {
		if p.closed != 1 {
			t.Error("apply ran before service close")
		}
		return failure
	}, nil)
	if !errors.Is(err, failure) || p.begin != 1 {
		t.Fatalf("failed update did not reopen service lifecycle: %v %d", err, p.begin)
	}
	if _, err := a.StartSession(a.GetOpenSessions()[0], dir); err != nil {
		t.Fatal(err)
	}
	a.Shutdown(context.Background())
}

type fakeCodexHistory struct {
	sessions []provider.Session
	err      error
}

func (h fakeCodexHistory) ListSessions(context.Context) ([]provider.Session, error) {
	return h.sessions, h.err
}

func installTestCodex(a *App, h fakeCodexHistory) {
	a.providers = a.newProviderRegistry(false)
	_ = a.providers.Register(provider.NewCodex(h))
	a.lookPath = func(name string) (string, error) { return name + ".exe", nil }
}

func TestCodexStartsWithSeparateIdentityAndTemporaryNewToken(t *testing.T) {
	a, _, _, _ := testApp(t)
	installTestCodex(a, fakeCodexHistory{})
	var command, token string
	a.startPTYFn = func(tok, cmd, dir string) error { token, command = tok, cmd; return nil }
	key := "codex:01900000-0000-7000-8000-000000000001"
	if got, err := a.StartSession(key, `C:\work`); err != nil || got != key {
		t.Fatalf("resume: %q %v", got, err)
	}
	if token != key || command != "cmd /c codex resume 01900000-0000-7000-8000-000000000001 --no-daemon" {
		t.Fatalf("launch %q %q", token, command)
	}
	got, err := a.StartNewAgent("codex", `C:\work`)
	if err != nil || !strings.HasPrefix(got, "new-codex-") || command != "cmd /c codex --no-daemon" {
		t.Fatalf("new: %q %q %v", got, command, err)
	}
	if err := a.AdoptSession(got, "01900000-0000-7000-8000-000000000001"); err == nil {
		t.Fatal("Codex temporary token accepted Claude adoption")
	}
}

func TestCodexHistoryAndLocalMutationsStaySeparateFromClaude(t *testing.T) {
	a, store, _, _ := testApp(t)
	id := "01900000-0000-7000-8000-000000000001"
	installTestCodex(a, fakeCodexHistory{sessions: []provider.Session{{Ref: provider.SessionRef{Provider: provider.Codex, ID: id}, Dir: `C:\work`, Name: "Native Codex title", Time: time.Unix(10, 0)}}})
	if err := store.SetAlias(id, "Legacy Claude alias"); err != nil {
		t.Fatal(err)
	}
	key := "codex:" + id
	list := a.ListSessions()
	if len(list) != 1 || list[0].ID != key || list[0].Provider != "codex" || list[0].RawID != id || list[0].Name != "Native Codex title" {
		t.Fatalf("list: %#v", list)
	}
	if err := a.DeleteSession(key); err != nil {
		t.Fatal(err)
	}
	if len(a.ListSessions()) != 0 || len(a.ListHiddenSessions()) != 1 {
		t.Fatal("local hide failed")
	}
	st, err := store.Load()
	if err != nil || st.HiddenSet()[id] || st.Aliases[id] != "Legacy Claude alias" {
		t.Fatalf("state: %#v %v", st, err)
	}
	if err := a.UnhideSession(key); err != nil {
		t.Fatal(err)
	}
	if len(a.ListSessions()) != 1 {
		t.Fatal("local restore failed")
	}
	if got := a.GetUsageSummary(key, `C:\work`); got.SessionFound || got.ProjectFound || len(got.Warnings) == 0 {
		t.Fatalf("Codex used Claude usage: %#v", got)
	}
}

func TestMixedProviderShutdownPersistStartupRestore(t *testing.T) {
	a, store, _, _ := testApp(t)
	installTestCodex(a, fakeCodexHistory{})
	a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(string, string, int, int, []string) (terminal.Pty, error) { return newShutdownPty(), nil })
	keys := []string{"01900000-0000-7000-8000-000000000001", "codex:01900000-0000-7000-8000-000000000001"}
	for _, key := range keys {
		if _, err := a.StartSession(key, `C:\work`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.StartNewAgent("codex", `C:\work`); err != nil {
		t.Fatal(err)
	}
	a.Shutdown(context.Background())
	restored := NewAppWithStore(store)
	installTestCodex(restored, fakeCodexHistory{})
	var commands []string
	restored.startPTYFn = func(_, cmd, _ string) error { commands = append(commands, cmd); return nil }
	if !reflect.DeepEqual(restored.GetOpenSessions(), keys) {
		t.Fatalf("persisted: %v", restored.GetOpenSessions())
	}
	for _, key := range restored.GetOpenSessions() {
		if _, err := restored.StartSession(key, `C:\work`); err != nil {
			t.Fatal(err)
		}
	}
	if len(commands) != 2 || !strings.Contains(commands[0], "claude -r ") || !strings.Contains(commands[1], "codex resume ") {
		t.Fatalf("restored: %v", commands)
	}
}

func TestUnknownOrMissingAgentNeverStartsPTY(t *testing.T) {
	a, _, _, _ := testApp(t)
	installTestCodex(a, fakeCodexHistory{})
	a.lookPath = func(string) (string, error) { return "", errors.New("missing") }
	started := false
	a.startPTYFn = func(string, string, string) error { started = true; return nil }
	for _, id := range []string{"codex", "unknown"} {
		if _, err := a.StartNewAgent(id, `C:\work`); err == nil {
			t.Fatalf("accepted %s", id)
		}
	}
	if started {
		t.Fatal("started PTY for unavailable provider")
	}
}
