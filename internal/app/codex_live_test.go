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

func TestCodexOwnedLaunchQueriesStatusUsageAndClosesOnShutdown(t *testing.T) {
	a, store, _, _ := testApp(t)
	var client, server func([]byte)
	var cleanup atomic.Int32
	live := provider.NewCodexLive(provider.NewCodex(nil), func(_ context.Context, _ string, c, s func([]byte), _ func()) (provider.LaunchSpec, error) {
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
	if cleanup.Load() != 1 || len(a.GetOpenSessions()) != 0 || len(a.GetAgents()) != 0 {
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
