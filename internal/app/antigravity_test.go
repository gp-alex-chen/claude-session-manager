package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gp-alex-chen/claude-session-manager/internal/provider"
	"github.com/gp-alex-chen/claude-session-manager/internal/state"
	"github.com/gp-alex-chen/claude-session-manager/internal/terminal"
)

func TestAntigravityStartsWithStalePathReportsStatusAndDoesNotPersistUnverifiedID(t *testing.T) {
	a, store, _, root := testApp(t)
	bin := filepath.Join(root, "CLI %special% & 中文 !", "agy.exe")
	if err := os.MkdirAll(filepath.Dir(bin), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("fixture; never run"), 0600); err != nil {
		t.Fatal(err)
	}
	p := provider.NewAntigravity(provider.AntigravityConfig{Lookup: func(string) (string, error) { return bin, nil }, HelperExecutable: bin, TempRoot: root})
	if err := a.providers.Register(p); err != nil {
		t.Fatal(err)
	}
	a.lookPath = func(string) (string, error) { return "", errors.New("stale PATH") }
	var captured []string
	a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(command, dir string, _, _ int, env []string) (terminal.Pty, error) {
		if command != `cmd /d /v:off /s /c ""%THEONE_AGY_EXE%" --settings "%THEONE_AGY_SETTINGS%""` {
			t.Errorf("unsafe launch: %s", command)
		}
		captured = append([]string(nil), env...)
		return newShutdownPty(), nil
	})
	token, err := a.StartNewAgent("antigravity", root)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot string
	for _, entry := range captured {
		if value, ok := strings.CutPrefix(entry, "THEONE_AGY_SNAPSHOT="); ok {
			snapshot = value
		}
	}
	if snapshot == "" {
		t.Fatal("status helper not connected")
	}
	if err := provider.WriteAntigravitySnapshot(strings.NewReader(`{"agent_state":"working","conversation_title":"Synthetic title","conversation_id":"12345678-abcd-ef01-2345-6789abcdef01"}`), snapshot); err != nil {
		t.Fatal(err)
	}
	list := a.GetAgents()
	if len(list) != 1 || list[0].RuntimeToken != token || list[0].State != "working" || list[0].Name != "Synthetic title" || list[0].Restorable {
		t.Fatalf("status not delivered: %#v", list)
	}
	if _, err := a.StartSession("antigravity:12345678-abcd-ef01-2345-6789abcdef01", root); err == nil {
		t.Fatal("unverified resume accepted")
	}
	a.Shutdown(context.Background())
	if got := a.GetOpenSessions(); len(got) != 0 {
		t.Fatalf("unverified native ID persisted: %v", got)
	}
	if _, err := os.Stat(filepath.Dir(snapshot)); !os.IsNotExist(err) {
		t.Fatal("terminal-owned runtime files leaked", err)
	}
	restarted := NewAppWithStore(store)
	if len(restarted.GetOpenSessionRefs()) != 0 {
		t.Fatal("temporary terminal restored after restart")
	}
}

func TestAntigravityRuntimeSettingsUsePackageIndependentHomeCache(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "package-specific-localappdata"))
	previous := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	defer func() { userHomeDir = previous }()
	bin := filepath.Join(root, "agy.exe")
	if err := os.WriteFile(bin, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	a := NewAppWithStore(state.NewStore(filepath.Join(root, "state")))
	a.lookPath = func(string) (string, error) { return bin, nil }
	spec, err := a.providers.PrepareLaunch(context.Background(), provider.Antigravity, provider.OpenRequest{RuntimeToken: "new-antigravity-cross-package", Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	defer spec.Close()
	if !strings.HasPrefix(spec.Args[1], filepath.Join(home, ".cache", "TheOne", "antigravity-runtime")+string(os.PathSeparator)) {
		t.Fatal("Store shell cannot share package-private AppData settings", spec.Args[1])
	}
}

type appAntigravityHistory struct{ sessions []provider.Session }

func (h appAntigravityHistory) ListSessions(context.Context) ([]provider.Session, error) {
	return h.sessions, nil
}

func TestAntigravityKnownHistoryShutdownPersistStartupAndManualReopen(t *testing.T) {
	a, store, _, root := testApp(t)
	bin := filepath.Join(root, "agy.exe")
	if err := os.WriteFile(bin, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	id := "01900000-0000-7000-8000-000000000001"
	key := "antigravity:" + id
	history := appAntigravityHistory{sessions: []provider.Session{{Ref: provider.SessionRef{Provider: provider.Antigravity, ID: id}, Dir: root, Name: "Durable native title"}}}
	install := func(app *App) *[]string {
		app.providers = app.newProviderRegistry(false)
		p := provider.NewAntigravity(provider.AntigravityConfig{Lookup: func(string) (string, error) { return bin, nil }, HelperExecutable: bin, TempRoot: root, History: history})
		if err := app.providers.Register(p); err != nil {
			t.Fatal(err)
		}
		captured := []string{}
		app.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(command, _ string, _, _ int, env []string) (terminal.Pty, error) {
			if !strings.Contains(command, "--conversation") {
				t.Error("history open became new session", command)
			}
			captured = append([]string(nil), env...)
			return newShutdownPty(), nil
		})
		return &captured
	}
	env := install(a)
	if _, err := a.StartSession(key, root); err != nil {
		t.Fatal(err)
	}
	if _, err := a.StartSession(key, root); err != nil {
		t.Fatal("same unbound native runtime was mistaken for a switched session", err)
	}
	if got := a.GetOpenSessions(); len(got) != 0 {
		t.Fatal("canonical token briefly persisted", got)
	}
	var snapshot string
	for _, entry := range *env {
		if v, ok := strings.CutPrefix(entry, "THEONE_AGY_SNAPSHOT="); ok {
			snapshot = v
		}
	}
	if err := provider.WriteAntigravitySnapshot(strings.NewReader(`{"conversation_id":"`+id+`","conversation_title":"Durable native title","agent_state":"idle"}`), snapshot); err != nil {
		t.Fatal(err)
	}
	if list := a.GetAgents(); len(list) != 1 || list[0].State != "idle" || list[0].Restorable {
		t.Fatal("known native identity not observed safely", list)
	}
	a.Shutdown(context.Background())
	restarted := NewAppWithStore(store)
	install(restarted)
	if refs := restarted.GetOpenSessionRefs(); len(refs) != 1 || refs[0].ID != key || refs[0].Provider != "antigravity" {
		t.Fatal("confirmed native conversation missing after restart", refs)
	}
	list, err := restarted.ListSessionsForProviders([]string{"antigravity"})
	if err != nil || len(list.Sessions) != 1 || list.Sessions[0].Dir != root || list.Sessions[0].Time != "时间未知" {
		t.Fatal("native metadata lost after restart", list, err)
	}
	if _, err := restarted.StartSession(list.Sessions[0].ID, list.Sessions[0].Dir); err != nil {
		t.Fatal(err)
	}
	restarted.Shutdown(context.Background())
	if len(restarted.GetOpenSessions()) != 0 {
		t.Fatal("manual reopen unexpectedly entered automatic restore list")
	}
}

type blockingAntigravityHistory struct {
	started, release chan struct{}
	sessions         []provider.Session
}

func (h blockingAntigravityHistory) ListSessions(context.Context) ([]provider.Session, error) {
	close(h.started)
	<-h.release
	return h.sessions, nil
}

func TestAntigravityClosingDuringHistoryPreflightCannotStartOrphanTerminal(t *testing.T) {
	a, store, _, root := testApp(t)
	defer a.Shutdown(context.Background())
	bin := filepath.Join(root, "agy.exe")
	if err := os.WriteFile(bin, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	id := "01900000-0000-7000-8000-000000000001"
	key := "antigravity:" + id
	h := blockingAntigravityHistory{started: make(chan struct{}), release: make(chan struct{}), sessions: []provider.Session{{Ref: provider.SessionRef{Provider: provider.Antigravity, ID: id}, Dir: root}}}
	p := provider.NewAntigravity(provider.AntigravityConfig{Lookup: func(string) (string, error) { return bin, nil }, HelperExecutable: bin, TempRoot: root, History: h})
	if err := a.providers.Register(p); err != nil {
		t.Fatal(err)
	}
	started := false
	a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(string, string, int, int, []string) (terminal.Pty, error) {
		started = true
		return newShutdownPty(), nil
	})
	done := make(chan error, 1)
	go func() { _, err := a.StartSession(key, root); done <- err }()
	<-h.started
	a.TermKill(key)
	close(h.release)
	if err := <-done; err == nil || started || a.terms.IsRunning(key) {
		t.Fatal("closed tab left a late-started terminal", started, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "theone-agy-") {
			t.Fatal("canceled launch leaked runtime", entry.Name())
		}
	}
	if len(a.GetOpenSessions()) != 0 {
		t.Fatal("canceled launch persisted")
	}
}

func blockedAntigravityLaunch(t *testing.T) (*App, *state.Store, string, blockingAntigravityHistory, <-chan error) {
	t.Helper()
	a, store, _, root := testApp(t)
	bin := filepath.Join(root, "agy.exe")
	if err := os.WriteFile(bin, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	id := "01900000-0000-7000-8000-000000000001"
	h := blockingAntigravityHistory{started: make(chan struct{}), release: make(chan struct{}), sessions: []provider.Session{{Ref: provider.SessionRef{Provider: provider.Antigravity, ID: id}, Dir: root}}}
	p := provider.NewAntigravity(provider.AntigravityConfig{Lookup: func(string) (string, error) { return bin, nil }, HelperExecutable: bin, TempRoot: root, History: h})
	if err := a.providers.Register(p); err != nil {
		t.Fatal(err)
	}
	a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(string, string, int, int, []string) (terminal.Pty, error) {
		return newShutdownPty(), nil
	})
	done := make(chan error, 1)
	go func() { _, err := a.StartSession("antigravity:"+id, root); done <- err }()
	<-h.started
	return a, store, root, h, done
}

func TestAntigravityHistoryPreflightDoesNotBlockIndependentLaunchOrStatus(t *testing.T) {
	a, _, root, history, opening := blockedAntigravityLaunch(t)
	defer a.Shutdown(context.Background())
	launched := make(chan error, 1)
	go func() { _, err := a.StartNewAgent("claude", root); launched <- err }()
	status := make(chan struct{})
	go func() { a.GetAgents(); close(status) }()
	launchReceived := false
	defer func() {
		close(history.release)
		<-opening
		if !launchReceived {
			<-launched
		}
		<-status
	}()
	select {
	case err := <-launched:
		launchReceived = true
		if err != nil {
			t.Fatal("independent launch:", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("unrelated launch waited for Antigravity history preflight")
	}
	select {
	case <-status:
	case <-time.After(3 * time.Second):
		t.Fatal("status query waited for Antigravity history preflight")
	}
}

func TestAntigravityShutdownDoesNotWaitForHistoryAndRejectsLatePreparation(t *testing.T) {
	a, store, root, history, opening := blockedAntigravityLaunch(t)
	shutdown := make(chan struct{})
	go func() { a.Shutdown(context.Background()); close(shutdown) }()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(history.release) }) }
	openReceived := false
	defer func() {
		release()
		if !openReceived {
			<-opening
		}
		<-shutdown
	}()
	select {
	case <-shutdown:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown waited for uncancellable history preparation")
	}
	// Model a fresh lifecycle while an old, cancellation-ignoring query returns.
	a.lifecycleMu.Lock()
	a.lifecycleID++
	a.ctx = context.Background()
	a.lifecycleMu.Unlock()
	release()
	err := <-opening
	openReceived = true
	if err == nil || len(a.terms.OpenIDs()) != 0 {
		t.Fatal("old preparation registered a terminal after shutdown/startup", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "theone-agy-") {
			t.Fatal("late preparation leaked runtime files", entry.Name())
		}
	}
	restarted := NewAppWithStore(store)
	if len(restarted.GetOpenSessionRefs()) != 0 {
		t.Fatal("late preparation entered restart recovery")
	}
}

type cleanupBlockedAntigravity struct {
	provider.ClaudeAdapter
	bin                                        string
	entered, release, cleaning, releaseCleanup chan struct{}
	reads                                      atomic.Int32
	owned                                      atomic.Bool
}

func (p *cleanupBlockedAntigravity) Descriptor() provider.Descriptor {
	return provider.Descriptor{ID: provider.Antigravity}
}

func (p *cleanupBlockedAntigravity) PrepareLaunch(context.Context, provider.OpenRequest) (provider.LaunchSpec, error) {
	first := p.reads.Add(1) == 1
	if first {
		close(p.entered)
		<-p.release // Deliberately ignore cancellation, like a late metadata source.
	}
	if p.owned.Swap(true) {
		return provider.LaunchSpec{}, errors.New("old preparation still owns runtime")
	}
	return provider.LaunchSpec{Command: "agy", Executable: p.bin, Args: []string{"--settings", "synthetic"}, Close: func() {
		if first {
			close(p.cleaning)
			<-p.releaseCleanup
		}
		p.owned.Store(false)
	}}, nil
}

func TestAntigravityReopenWaitsForCanceledPreparationCleanup(t *testing.T) {
	a, store, _, root := testApp(t)
	defer a.Shutdown(context.Background())
	p := &cleanupBlockedAntigravity{bin: filepath.Join(root, "agy.exe"), entered: make(chan struct{}), release: make(chan struct{}), cleaning: make(chan struct{}), releaseCleanup: make(chan struct{})}
	if err := os.WriteFile(p.bin, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.providers.Register(p); err != nil {
		t.Fatal(err)
	}
	a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(string, string, int, int, []string) (terminal.Pty, error) { return newShutdownPty(), nil })
	key := "antigravity:01900000-0000-7000-8000-000000000001"
	oldDone := make(chan error, 1)
	go func() { _, err := a.StartSession(key, root); oldDone <- err }()
	<-p.entered
	a.TermKill(key)
	close(p.release)
	<-p.cleaning
	newDone := make(chan error, 1)
	go func() { _, err := a.StartSession(key, root); newDone <- err }()
	// The replacement is allowed to wait only for this token. While cleanup
	// is held, its preparation must not fail on the old token's resource.
	var earlyErr error
	early := false
	select {
	case earlyErr = <-newDone:
		early = true
	case <-time.After(time.Second):
	}
	close(p.releaseCleanup)
	if err := <-oldDone; err == nil {
		t.Error("canceled preparation started a terminal")
	}
	if early {
		t.Fatal("replacement ran before old cleanup finished", earlyErr)
	}
	if err := <-newDone; err != nil || !a.terms.IsRunning(key) || !p.owned.Load() {
		t.Fatal("replacement did not acquire the cleaned runtime", err)
	}
	a.TermKill(key)
	if p.owned.Load() || len(a.GetOpenSessions()) != 0 {
		t.Fatal("replacement resource leaked or entered recovery")
	}
}

func TestAntigravityQueuedReplacementsRemainCancellableAndDoNotAffectOtherTokens(t *testing.T) {
	a, _, _, _ := testApp(t)
	begin := func(token string) *pendingAgentLaunch {
		t.Helper()
		pending, err := a.beginAgentLaunch(token, context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return pending
	}
	old := begin("same-token")
	a.TermKill("same-token")
	queued := begin("same-token")
	a.TermKill("same-token")
	latest := begin("same-token")
	a.TermKill("same-token")
	if queued.ctx.Err() == nil || latest.ctx.Err() == nil {
		t.Fatal("close missed a queued replacement")
	}
	started := 0
	start := func() error { started++; return nil }
	independent := begin("other-token")
	if err := a.commitAgentLaunch("other-token", independent, start); err != nil {
		t.Fatal("unrelated token blocked", err)
	}
	a.finishAgentLaunch("other-token", independent)
	a.finishAgentLaunch("same-token", old)
	a.finishAgentLaunch("same-token", queued)
	if err := a.commitAgentLaunch("same-token", latest, start); err == nil {
		t.Fatal("closed queued request started a terminal")
	}
	a.finishAgentLaunch("same-token", latest)
	reopened := begin("same-token")
	if err := a.commitAgentLaunch("same-token", reopened, start); err != nil {
		t.Fatal("cleanup prevented a fresh reopen", err)
	}
	a.finishAgentLaunch("same-token", reopened)
	if started != 2 {
		t.Fatal("unexpected terminal registration count", started)
	}
}

func TestAntigravityQuotaReadsNewCallbacksAndClearsAfterTerminalClose(t *testing.T) {
	a, store, _, root := testApp(t)
	defer a.Shutdown(context.Background())
	bin := filepath.Join(root, "agy.exe")
	if err := os.WriteFile(bin, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	p := provider.NewAntigravity(provider.AntigravityConfig{Lookup: func(string) (string, error) { return bin, nil }, HelperExecutable: bin, TempRoot: root})
	if err := a.providers.Register(p); err != nil {
		t.Fatal(err)
	}
	if q, err := a.GetAccountQuota("antigravity", false); err != nil || q.Status == "available" {
		t.Fatal(q, err)
	}
	var snapshot string
	starts := 0
	a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(_ string, _ string, _, _ int, env []string) (terminal.Pty, error) {
		starts++
		for _, entry := range env {
			if value, ok := strings.CutPrefix(entry, "THEONE_AGY_SNAPSHOT="); ok {
				snapshot = value
			}
		}
		return newShutdownPty(), nil
	})
	token, err := a.StartNewAgent("antigravity", root)
	if err != nil {
		t.Fatal(err)
	}
	for _, fraction := range []string{"0.75", "0"} {
		if err := provider.WriteAntigravitySnapshot(strings.NewReader(`{"agent_state":"idle","quota":{"synthetic-model":{"remaining_fraction":`+fraction+`}}}`), snapshot); err != nil {
			t.Fatal(err)
		}
		q, err := a.GetAccountQuota("antigravity", false)
		if err != nil || q.Status != "available" || len(q.Windows) != 1 || q.ReadAt.IsZero() {
			t.Fatal(q, err)
		}
		if (fraction == "0" && q.Windows[0].UsedPercent != 100) || (fraction == "0.75" && q.Windows[0].UsedPercent != 25) {
			t.Fatal("new callback hidden by cache", q)
		}
		before := q.ReadAt
		q, err = a.GetAccountQuota("antigravity", true)
		if err != nil || !q.ReadAt.Equal(before) || starts != 1 {
			t.Fatal("read changed collection time or launched CLI", q, err)
		}
	}
	a.TermKill(token)
	if q, err := a.GetAccountQuota("antigravity", false); err != nil || q.Status == "available" || len(q.Windows) != 0 {
		t.Fatal("closed callback remained available", q, err)
	}
}

func TestAntigravityNewConversationShutdownPersistStartupRestoreAndUserClose(t *testing.T) {
	a, store, _, root := testApp(t)
	bin := filepath.Join(root, "agy.exe")
	if err := os.WriteFile(bin, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	id := "01900000-0000-7000-8000-000000000001"
	key := "antigravity:" + id
	type launched struct{ snapshot, conversation, command string }
	install := func(app *App) *launched {
		app.providers = app.newProviderRegistry(false)
		p := provider.NewAntigravity(provider.AntigravityConfig{Lookup: func(string) (string, error) { return bin, nil }, HelperExecutable: bin, TempRoot: root,
			History: codexHistoryFunc(func(ctx context.Context) ([]provider.Session, error) {
				// Model a metadata transport needing ten seconds of budget,
				// without sleeping or consulting the installed CLI. Exit must
				// not truncate the normal fifteen-second catalog allowance.
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) < 10*time.Second {
					return nil, context.DeadlineExceeded
				}
				return []provider.Session{{Ref: provider.SessionRef{Provider: provider.Antigravity, ID: id}, Dir: root, Name: "Saved synthetic context"}}, nil
			})})
		if err := app.providers.Register(p); err != nil {
			t.Fatal(err)
		}
		captured := &launched{}
		app.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(command, _ string, _, _ int, env []string) (terminal.Pty, error) {
			captured.command = command
			for _, entry := range env {
				if v, ok := strings.CutPrefix(entry, "THEONE_AGY_SNAPSHOT="); ok {
					captured.snapshot = v
				}
				if v, ok := strings.CutPrefix(entry, "THEONE_AGY_CONVERSATION="); ok {
					captured.conversation = v
				}
			}
			return newShutdownPty(), nil
		})
		return captured
	}
	observe := func(run *launched) {
		t.Helper()
		if err := provider.WriteAntigravitySnapshot(strings.NewReader(`{"conversation_id":"`+id+`","agent_state":"idle"}`), run.snapshot); err != nil {
			t.Fatal(err)
		}
	}
	run := install(a)
	if _, err := a.StartNewAgent("antigravity", root); err != nil {
		t.Fatal(err)
	}
	observe(run)
	// No UI GetAgents call is required to save the final native reference.
	a.Shutdown(context.Background())
	a.Shutdown(context.Background())
	restarted := NewAppWithStore(store)
	run = install(restarted)
	refs := restarted.GetOpenSessionRefs()
	if len(refs) != 1 || refs[0].ID != key {
		t.Fatal("new conversation not restored", refs)
	}
	history, err := restarted.ListSessionsForProviders([]string{"antigravity"})
	if err != nil || len(history.Sessions) != 1 {
		t.Fatal(history, err)
	}
	if _, err := restarted.StartSession(refs[0].ID, history.Sessions[0].Dir); err != nil {
		t.Fatal(err)
	}
	if run.conversation != id || !strings.Contains(run.command, "--conversation") {
		t.Fatal("startup silently opened a new conversation", run)
	}
	observe(run)
	restarted.Shutdown(context.Background())
	third := NewAppWithStore(store)
	run = install(third)
	if len(third.GetOpenSessionRefs()) != 1 {
		t.Fatal("restored conversation was not saved again")
	}
	if _, err := third.StartSession(key, root); err != nil {
		t.Fatal(err)
	}
	observe(run)
	third.TermKill(key)
	third.Shutdown(context.Background())
	if refs := NewAppWithStore(store).GetOpenSessionRefs(); len(refs) != 0 {
		t.Fatal("user-closed terminal revived after startup", refs)
	}
}

func TestAntigravityOldShutdownCannotCloseNewLifecycleAfterSlowRestoreQuery(t *testing.T) {
	a, store, _, root := testApp(t)
	bin := filepath.Join(root, "agy.exe")
	if err := os.WriteFile(bin, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	id := "01900000-0000-7000-8000-000000000001"
	h := blockingAntigravityHistory{started: make(chan struct{}), release: make(chan struct{}), sessions: []provider.Session{{Ref: provider.SessionRef{Provider: provider.Antigravity, ID: id}, Dir: root}}}
	p := provider.NewAntigravity(provider.AntigravityConfig{Lookup: func(string) (string, error) { return bin, nil }, HelperExecutable: bin, TempRoot: root, History: h})
	if err := a.providers.Register(p); err != nil {
		t.Fatal(err)
	}
	var snapshot string
	a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(_, _ string, _, _ int, env []string) (terminal.Pty, error) {
		for _, entry := range env {
			if v, ok := strings.CutPrefix(entry, "THEONE_AGY_SNAPSHOT="); ok {
				snapshot = v
			}
		}
		return newShutdownPty(), nil
	})
	oldManager := a.terms
	if _, err := a.StartNewAgent("antigravity", root); err != nil {
		t.Fatal(err)
	}
	if err := provider.WriteAntigravitySnapshot(strings.NewReader(`{"conversation_id":"`+id+`"}`), snapshot); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { a.Shutdown(context.Background()); close(done) }()
	<-h.started
	// The same synchronized lifecycle transition used by Startup, without
	// installing Wails callbacks or starting real user-state watchers.
	a.launchMu.Lock()
	a.lifecycleMu.Lock()
	a.lifecycleID++
	a.stopping = false
	a.ctx = context.Background()
	a.lifecycleMu.Unlock()
	a.launchMu.Unlock()
	a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(string, string, int, int, []string) (terminal.Pty, error) { return newShutdownPty(), nil })
	token, err := a.StartNewAgent("claude", root)
	if err != nil {
		t.Fatal(err)
	}
	close(h.release)
	<-done
	if !a.terms.IsRunning(token) {
		t.Fatal("old shutdown closed a new lifecycle terminal")
	}
	oldManager.CloseAll()
	a.Shutdown(context.Background())
}

func observedAntigravityForExit(t *testing.T, history provider.HistoryProvider, checkSave func([]string) error) (*App, *state.Store, context.CancelFunc, string) {
	t.Helper()
	a, store, _, root := testApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	a.ctx = ctx
	a.cancelLifecycle = cancel
	bin := filepath.Join(root, "agy.exe")
	if err := os.WriteFile(bin, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	id := "01900000-0000-7000-8000-000000000001"
	if history == nil {
		history = appAntigravityHistory{sessions: []provider.Session{{Ref: provider.SessionRef{Provider: provider.Antigravity, ID: id}, Dir: root}}}
	}
	if h, ok := history.(*blockingAntigravityHistory); ok {
		h.sessions = []provider.Session{{Ref: provider.SessionRef{Provider: provider.Antigravity, ID: id}, Dir: root}}
	}
	p := provider.NewAntigravity(provider.AntigravityConfig{Lookup: func(string) (string, error) { return bin, nil }, HelperExecutable: bin, TempRoot: root, History: history})
	if err := a.providers.Register(p); err != nil {
		t.Fatal(err)
	}
	var snapshot string
	a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, func(ids []string) error {
		if checkSave != nil {
			if err := checkSave(ids); err != nil {
				return err
			}
		}
		return store.SaveOpen(ids)
	}, func(_, _ string, _, _ int, env []string) (terminal.Pty, error) {
		for _, entry := range env {
			if v, ok := strings.CutPrefix(entry, "THEONE_AGY_SNAPSHOT="); ok {
				snapshot = v
			}
		}
		return newShutdownPty(), nil
	})
	token, err := a.StartNewAgent("antigravity", root)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.WriteAntigravitySnapshot(strings.NewReader(`{"conversation_id":"`+id+`"}`), snapshot); err != nil {
		t.Fatal(err)
	}
	return a, store, cancel, token
}

func TestAntigravityExitSaveSurvivesCancelledLifecycleContext(t *testing.T) {
	a, store, cancel, _ := observedAntigravityForExit(t, nil, nil)
	cancel()
	a.Shutdown(context.Background())
	if refs := NewAppWithStore(store).GetOpenSessionRefs(); len(refs) != 1 || refs[0].Provider != "antigravity" {
		t.Fatal("cancelled UI context discarded durable native identity", refs)
	}
}

func TestAntigravityUpdateStopsWhenFinalNativeReferenceSaveFails(t *testing.T) {
	saveFailure := errors.New("synthetic native-reference save failure")
	var failNativeSave atomic.Bool
	failNativeSave.Store(true)
	a, store, cancel, token := observedAntigravityForExit(t, nil, func(ids []string) error {
		if len(ids) > 0 && failNativeSave.Load() {
			return saveFailure
		}
		return nil
	})
	defer cancel()
	defer a.Shutdown(context.Background())
	applied := false
	ready := false
	apply := func(string, bool) error { applied = true; return nil }
	onReady := func() { ready = true }
	err := a.applyDownloadedUpdate("synthetic", apply, onReady)
	if !errors.Is(err, saveFailure) || applied || ready || !a.terms.IsRunning(token) {
		t.Fatalf("failed update must keep the original terminal: err=%v applied=%v ready=%v running=%v", err, applied, ready, a.terms.IsRunning(token))
	}
	failNativeSave.Store(false)
	if err := a.terms.RetryPersistence(); err != nil {
		t.Fatal(err)
	}
	if saved, err := store.LoadOpen(); err != nil || len(saved) != 0 {
		t.Fatalf("a failed update leaked tentative native references: saved=%v err=%v", saved, err)
	}
	if err := a.applyDownloadedUpdate("synthetic", apply, onReady); err != nil || !applied || !ready || a.terms.IsRunning(token) {
		t.Fatalf("update retry did not save and close the terminal: err=%v applied=%v ready=%v running=%v", err, applied, ready, a.terms.IsRunning(token))
	}
	a.Shutdown(context.Background())
	want := "antigravity:01900000-0000-7000-8000-000000000001"
	if refs := NewAppWithStore(store).GetOpenSessionRefs(); len(refs) != 1 || refs[0].ID != want {
		t.Fatalf("retry did not preserve the native reference across shutdown/startup: %v", refs)
	}
}

func TestAntigravityShutdownWaitsForUpdateReferenceSave(t *testing.T) {
	h := &blockingAntigravityHistory{started: make(chan struct{}), release: make(chan struct{})}
	a, store, cancel, _ := observedAntigravityForExit(t, h, nil)
	defer cancel()
	updateDone := make(chan error, 1)
	go func() {
		updateDone <- a.applyDownloadedUpdate("synthetic", func(string, bool) error { return errors.New("synthetic replacement failure") }, nil)
	}()
	<-h.started
	shutdownDone := make(chan struct{})
	go func() { a.Shutdown(context.Background()); close(shutdownDone) }()
	early := false
	select {
	case <-shutdownDone:
		early = true
	case <-time.After(time.Second):
	}
	close(h.release)
	<-updateDone
	<-shutdownDone
	if early {
		t.Fatal("window shutdown returned before update finished saving")
	}
	if refs := NewAppWithStore(store).GetOpenSessionRefs(); len(refs) != 1 {
		t.Fatal("overlapping update and shutdown lost restore reference", refs)
	}
}

func TestAntigravityShutdownWaitsForLongStopOwner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, store, cancel, _ := observedAntigravityForExit(t, nil, nil)
		defer cancel()
		generation, ownerDone, owner := a.claimStop()
		if !owner {
			t.Fatal("could not claim stop owner")
		}
		shutdownDone := make(chan struct{})
		go func() { a.Shutdown(context.Background()); close(shutdownDone) }()
		synctest.Wait()
		// The fake clock models a busy update owner without delaying the test.
		<-time.After(20 * time.Second)
		select {
		case <-shutdownDone:
			t.Error("shutdown returned before the stop owner saved/released")
		default:
		}
		a.completeStop(generation, ownerDone, true)
		<-shutdownDone
		refs := NewAppWithStore(store).GetOpenSessionRefs()
		if len(refs) != 1 || refs[0].Provider != "antigravity" {
			t.Fatal("waiting shutdown lost native restoration", refs)
		}
	})
}
