package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gp-alex-chen/claude-session-manager/internal/provider"
	"github.com/gp-alex-chen/claude-session-manager/internal/terminal"
)

// The fake relay speaks the same identity messages as the native TUI. No CLI,
// user history, sleeps, or frontend polling are involved in this lifecycle.
func TestNewCodexIdentityPersistsThroughShutdownAndNativeRestore(t *testing.T) {
	for _, early := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-shutdown-without-poll", true: "before-PTY-registration"}[early], func(t *testing.T) {
			a, store, _, _ := testApp(t)
			id := "01900000-0000-7000-8000-000000000001"
			key := "codex:" + id
			var sendIdentity func()
			var savedHistory []provider.Session
			if early {
				savedHistory = []provider.Session{{Ref: provider.SessionRef{Provider: provider.Codex, ID: id}, Dir: `C:\work`}}
			}
			live := provider.NewCodexLive(provider.NewCodex(codexHistoryFunc(func(context.Context) ([]provider.Session, error) { return savedHistory, nil })), func(_ context.Context, _ string, client, server func([]byte), _ func()) (provider.LaunchSpec, error) {
				sendIdentity = func() {
					client([]byte(`{"id":1,"method":"thread/start","params":{}}`))
					server([]byte(`{"id":1,"result":{"thread":{"id":"` + id + `","status":{"type":"idle"}}}}`))
				}
				if early {
					sendIdentity()
				}
				return provider.LaunchSpec{Command: "codex", Close: func() {}}, nil
			}, a.signalProviderUpdate)
			a.providers = provider.NewRegistry()
			_ = a.providers.Register(live)
			a.lookPath = func(string) (string, error) { return "codex.exe", nil }
			a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(string, string, int, int, []string) (terminal.Pty, error) { return newShutdownPty(), nil })
			token, err := a.StartNewAgent("codex", `C:\work`)
			if err != nil {
				t.Fatal(err)
			}
			if !early {
				sendIdentity()
				if agents := a.GetAgents(); len(agents) != 1 || agents[0].Restorable || len(a.GetOpenSessions()) != 0 {
					t.Fatal("empty native thread was prematurely saved", agents)
				}
				// The message was saved after a negative cache entry. Shutdown
				// must bypass that entry even without another frontend poll.
				savedHistory = []provider.Session{{Ref: provider.SessionRef{Provider: provider.Codex, ID: id}, Dir: `C:\work`}}
			}
			a.Shutdown(context.Background())
			restored := NewAppWithStore(store)
			installTestCodex(restored, fakeCodexHistory{})
			if got := restored.GetOpenSessions(); !reflect.DeepEqual(got, []string{key}) {
				t.Fatalf("new terminal %s lost native identity at shutdown: %v", token, got)
			}
			var command string
			restored.startPTYFn = func(_, cmd, _ string) error { command = cmd; return nil }
			if _, err := restored.StartSession(key, `C:\work`); err != nil || !strings.Contains(command, "codex resume "+id) {
				t.Fatalf("native restore command=%q err=%v", command, err)
			}
		})
	}
}

type codexHistoryFunc func(context.Context) ([]provider.Session, error)

func (h codexHistoryFunc) ListSessions(ctx context.Context) ([]provider.Session, error) {
	return h(ctx)
}

func TestCodexBindingWaitsForSuccessfulPersistenceAndCannotReviveAfterKill(t *testing.T) {
	a, store, _, _ := testApp(t)
	id := "01900000-0000-7000-8000-000000000001"
	var client, server func([]byte)
	live := provider.NewCodexLive(provider.NewCodex(fakeCodexHistory{sessions: []provider.Session{{Ref: provider.SessionRef{Provider: provider.Codex, ID: id}, Dir: `C:\work`}}}), func(_ context.Context, _ string, c, s func([]byte), _ func()) (provider.LaunchSpec, error) {
		client, server = c, s
		return provider.LaunchSpec{Command: "codex", Close: func() {}}, nil
	}, nil)
	a.providers = provider.NewRegistry()
	_ = a.providers.Register(live)
	a.lookPath = func(string) (string, error) { return "codex.exe", nil }
	var fail atomic.Bool
	a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, func(ids []string) error {
		if fail.Load() {
			return errors.New("synthetic disk failure")
		}
		return store.SaveOpen(ids)
	}, func(string, string, int, int, []string) (terminal.Pty, error) { return newShutdownPty(), nil })
	token, err := a.StartNewAgent("codex", `C:\work`)
	if err != nil {
		t.Fatal(err)
	}
	client([]byte(`{"id":1,"method":"thread/start","params":{}}`))
	server([]byte(`{"id":1,"result":{"thread":{"id":"` + id + `","status":{"type":"idle"}}}}`))
	fail.Store(true)
	if list := a.GetAgents(); len(list) != 1 || list[0].Restorable || len(a.GetOpenSessions()) != 0 {
		t.Fatal("failed disk write claimed recoverable", list)
	}
	fail.Store(false)
	if list := a.GetAgents(); len(list) != 1 || !list[0].Restorable || !reflect.DeepEqual(a.GetOpenSessions(), []string{"codex:" + id}) {
		t.Fatal("persistence retry failed", list, a.GetOpenSessions())
	}
	a.TermKill(token)
	server([]byte(`{"method":"thread/status/changed","params":{"threadId":"` + id + `","status":{"type":"idle"}}}`))
	a.Shutdown(context.Background())
	if len(a.GetOpenSessions()) != 0 {
		t.Fatal("late native event revived killed session", a.GetOpenSessions())
	}
}

func TestCodexOwnedLaunchQueriesStatusUsageAndClosesOnShutdown(t *testing.T) {
	a, store, _, _ := testApp(t)
	var client, server func([]byte)
	var cleanup atomic.Int32
	live := provider.NewCodexLive(provider.NewCodex(fakeCodexHistory{sessions: []provider.Session{{Ref: provider.SessionRef{Provider: provider.Codex, ID: "01900000-0000-7000-8000-000000000001"}, Dir: `C:\work`}}}), func(_ context.Context, _ string, c, s func([]byte), _ func()) (provider.LaunchSpec, error) {
		client, server = c, s
		return provider.LaunchSpec{Command: "codex", Args: []string{"--remote", "ws://127.0.0.1:1234"}, Env: []string{"FYNE_TEST_TOKEN=synthetic"}, Close: func() { cleanup.Add(1) }}, nil
	}, nil)
	a.providers = provider.NewRegistry()
	if err := a.providers.Register(live); err != nil {
		t.Fatal(err)
	}
	a.lookPath = func(string) (string, error) { return "codex.exe", nil }
	a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(cmd, _ string, _, _ int, env []string) (terminal.Pty, error) {
		if !strings.Contains(cmd, "--remote ws://127.0.0.1:1234") {
			t.Fatal("not native remote", cmd)
		}
		found := false
		for _, value := range env {
			if value == "FYNE_TEST_TOKEN=synthetic" {
				found = true
			}
		}
		if !found {
			t.Fatal("missing private environment")
		}
		return newShutdownPty(), nil
	})
	token, err := a.StartNewAgent("codex", `C:\work`)
	if err != nil {
		t.Fatal(err)
	}
	id := "01900000-0000-7000-8000-000000000001"
	client([]byte(`{"id":1,"method":"thread/start","params":{}}`))
	server([]byte(`{"id":1,"result":{"thread":{"id":"` + id + `","status":{"type":"active","activeFlags":[]}}}}`))
	server([]byte(`{"method":"thread/tokenUsage/updated","params":{"threadId":"` + id + `","tokenUsage":{"total":{"totalTokens":12,"inputTokens":10,"outputTokens":2},"last":{"totalTokens":12,"inputTokens":10,"outputTokens":2}}}}`))
	agents := a.GetAgents()
	if len(agents) != 1 || agents[0].SessionID != "codex:"+id || agents[0].RuntimeToken != token || agents[0].State != "working" {
		t.Fatal(agents)
	}
	summary := a.GetUsageSummary(token, `C:\work`)
	if !summary.SessionFound || summary.SessionTotal.TotalTokens() != 12 {
		t.Fatal(summary)
	}
	a.Shutdown(context.Background())
	if cleanup.Load() != 1 || !reflect.DeepEqual(a.GetOpenSessions(), []string{"codex:" + id}) || len(a.GetAgents()) != 0 {
		t.Fatalf("cleanup=%d open=%v agents=%v", cleanup.Load(), a.GetOpenSessions(), a.GetAgents())
	}
}

func TestCodexOwnedShutdownPersistsBeforeRuntimeCancellationAndRestores(t *testing.T) {
	a, store, _, _ := testApp(t)
	installTestCodex(a, fakeCodexHistory{})
	a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(string, string, int, int, []string) (terminal.Pty, error) { return newShutdownPty(), nil })
	key := "codex:01900000-0000-7000-8000-000000000001"
	if _, err := a.StartSession(key, `C:\work`); err != nil {
		t.Fatal(err)
	}
	// Model the owned server's cancellation synchronously terminating its
	// attached PTY. This makes a shutdown ordering regression deterministic.
	a.cancelLifecycle = func() { a.terms.Kill(key) }
	a.Shutdown(context.Background())
	restored := NewAppWithStore(store)
	installTestCodex(restored, fakeCodexHistory{})
	if !reflect.DeepEqual(restored.GetOpenSessions(), []string{key}) {
		t.Fatal("runtime exit erased restore snapshot", restored.GetOpenSessions())
	}
	var command string
	restored.startPTYFn = func(_, cmd, _ string) error { command = cmd; return nil }
	if _, err := restored.StartSession(restored.GetOpenSessions()[0], `C:\work`); err != nil || !strings.Contains(command, "codex resume ") {
		t.Fatalf("restore %q %v", command, err)
	}
}

func TestCodexPTYFailureReleasesPreparedTransport(t *testing.T) {
	a, _, _, _ := testApp(t)
	var cleanup atomic.Int32
	live := provider.NewCodexLive(provider.NewCodex(nil), func(context.Context, string, func([]byte), func([]byte), func()) (provider.LaunchSpec, error) {
		return provider.LaunchSpec{Command: "codex", Close: func() { cleanup.Add(1) }}, nil
	}, nil)
	a.providers = provider.NewRegistry()
	_ = a.providers.Register(live)
	a.lookPath = func(string) (string, error) { return "codex.exe", nil }
	a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, nil, func(string, string, int, int, []string) (terminal.Pty, error) { return nil, errors.New("PTY failed") })
	if _, err := a.StartNewAgent("codex", `C:\work`); err == nil {
		t.Fatal("accepted failed PTY")
	}
	if cleanup.Load() != 1 || len(a.GetOpenSessions()) != 0 {
		t.Fatalf("cleanup=%d open=%v", cleanup.Load(), a.GetOpenSessions())
	}
}

func TestShutdownRejectsLateTerminalLaunch(t *testing.T) {
	a, _, _, _ := testApp(t)
	installTestCodex(a, fakeCodexHistory{})
	started := false
	a.startPTYFn = func(string, string, string) error { started = true; return nil }
	a.Shutdown(context.Background())
	if _, err := a.StartNewAgent("codex", `C:\work`); err == nil || started {
		t.Fatalf("late launch err=%v started=%v", err, started)
	}
}
