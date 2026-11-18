package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gp-alex-chen/claude-session-manager/internal/provider"
	"github.com/gp-alex-chen/claude-session-manager/internal/terminal"
)

func TestStartupRestoreOneCatalogForMultipleAntigravitySessionsAndLifecycle(t *testing.T) {
	a, store, _, root := testApp(t)
	bin := filepath.Join(root, "agy.exe")
	if err := os.WriteFile(bin, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	ids := []string{"01900000-0000-7000-8000-000000000001", "01900000-0000-7000-8000-000000000002"}
	rows := []provider.Session{}
	keys := []string{}
	for _, id := range ids {
		rows = append(rows, provider.Session{Ref: provider.SessionRef{Provider: provider.Antigravity, ID: id}, Dir: root})
		keys = append(keys, "antigravity:"+id)
	}
	reads := 0
	snapshots := map[string]string{}
	install := func(a *App) {
		a.providers = a.newProviderRegistry(false)
		p := provider.NewAntigravity(provider.AntigravityConfig{Lookup: func(string) (string, error) { return bin, nil }, HelperExecutable: bin, TempRoot: root, History: codexHistoryFunc(func(context.Context) ([]provider.Session, error) { reads++; return rows, nil })})
		if err := a.providers.Register(p); err != nil {
			t.Fatal(err)
		}
		a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(command, dir string, _, _ int, env []string) (terminal.Pty, error) {
			var id, path string
			for _, entry := range env {
				if v, ok := strings.CutPrefix(entry, "THEONE_AGY_CONVERSATION="); ok {
					id = v
				}
				if v, ok := strings.CutPrefix(entry, "THEONE_AGY_SNAPSHOT="); ok {
					path = v
				}
			}
			if id == "" || !strings.Contains(command, "--conversation") || dir != root {
				return nil, errors.New("restore lost exact native ID/directory")
			}
			snapshots[id] = path
			return newShutdownPty(), nil
		})
	}
	install(a)
	if err := store.SaveOpen(keys); err != nil {
		t.Fatal(err)
	}
	for cycle := 0; cycle < 2; cycle++ {
		before := reads
		batch, err := a.BeginSessionRestore("antigravity")
		if err != nil || batch.Token == "" || len(batch.History.Sessions) != 2 {
			t.Fatalf("batch=%+v err=%v", batch, err)
		}
		if _, err := a.RestoreSession("forged", keys[0]); err == nil {
			t.Fatal("accepted forged token")
		}
		for _, key := range keys {
			if _, err := a.RestoreSession(batch.Token, key); err != nil {
				t.Fatal(err)
			}
		}
		if reads != before+1 {
			t.Fatalf("startup read %d catalogs for two sessions", reads-before)
		}
		if _, err := a.RestoreSession(batch.Token, keys[0]); err == nil {
			t.Fatal("consumed restore ID reused")
		}
		for _, id := range ids {
			if err := provider.WriteAntigravitySnapshot(strings.NewReader(`{"conversation_id":"`+id+`","agent_state":"idle"}`), snapshots[id]); err != nil {
				t.Fatal(err)
			}
		}
		a.EndSessionRestore(batch.Token)
		if _, err := a.RestoreSession(batch.Token, keys[0]); err == nil {
			t.Fatal("ended token accepted")
		}
		a.Shutdown(context.Background())
		a = NewAppWithStore(store)
		install(a)
		if refs := a.GetOpenSessionRefs(); len(refs) != 2 {
			t.Fatal("shutdown/persist/startup lost references", refs)
		}
	}
	batch, err := a.BeginSessionRestore("antigravity")
	if err != nil {
		t.Fatal(err)
	}
	a.TermKill(keys[0])
	if _, err := a.RestoreSession(batch.Token, keys[0]); err == nil {
		t.Fatal("close did not revoke old startup authorization")
	}
	a.EndSessionRestore(batch.Token)
	// Manual opens retain fresh verification, independent of startup snapshots.
	before := reads
	if _, err := a.StartSession(keys[0], root); err != nil {
		t.Fatal(err)
	}
	if reads != before+1 {
		t.Fatal("manual open reused startup history")
	}
	a.TermKill(keys[0])
	a.Shutdown(context.Background())
}

func TestStartupRestoreCancellationExpiryAndGeneration(t *testing.T) {
	for _, mode := range []string{"close", "end", "new-batch", "new-lifecycle", "expired", "source-failure", "unsaved"} {
		t.Run(mode, func(t *testing.T) {
			a, store, _, root := testApp(t)
			bin := filepath.Join(root, "agy.exe")
			os.WriteFile(bin, []byte("fixture"), 0600)
			id := "01900000-0000-7000-8000-000000000001"
			key := "antigravity:" + id
			entered, release := make(chan struct{}), make(chan struct{})
			var block atomic.Bool
			failed := false
			p := provider.NewAntigravity(provider.AntigravityConfig{Lookup: func(string) (string, error) {
				if block.Load() {
					close(entered)
					<-release
				}
				return bin, nil
			}, HelperExecutable: bin, TempRoot: root, History: codexHistoryFunc(func(context.Context) ([]provider.Session, error) {
				if failed {
					return nil, errors.New("synthetic query failure")
				}
				return []provider.Session{{Ref: provider.SessionRef{Provider: provider.Antigravity, ID: id}, Dir: root}}, nil
			})})
			a.providers = a.newProviderRegistry(false)
			a.providers.Register(p)
			starts := 0
			a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(string, string, int, int, []string) (terminal.Pty, error) { starts++; return newShutdownPty(), nil })
			if mode != "unsaved" {
				store.SaveOpen([]string{key})
			}
			batch, err := a.BeginSessionRestore("antigravity")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "source-failure" {
				failed = true
				if b, e := a.BeginSessionRestore("antigravity"); e == nil || b.Token != "" {
					t.Fatal("failed query issued authorization")
				}
			}
			if mode == "new-batch" {
				if _, e := a.BeginSessionRestore("antigravity"); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "new-lifecycle" {
				a.lifecycleMu.Lock()
				a.lifecycleID++
				a.lifecycleMu.Unlock()
			}
			if mode == "expired" {
				a.restoreMu.Lock()
				a.restoreBatches[batch.Token].expires = time.Now().Add(-time.Second)
				a.restoreMu.Unlock()
			}
			if mode == "close" || mode == "end" {
				block.Store(true)
				done := make(chan error, 1)
				go func() { _, e := a.RestoreSession(batch.Token, key); done <- e }()
				<-entered
				if mode == "close" {
					a.TermKill(key)
				} else {
					a.EndSessionRestore(batch.Token)
				}
				block.Store(false)
				close(release)
				if e := <-done; e == nil {
					t.Fatal("canceled preparation returned success")
				}
			} else if _, e := a.RestoreSession(batch.Token, key); e == nil {
				t.Fatal("invalid old authorization accepted")
			}
			if starts != 0 {
				t.Fatal("invalidated restore created a PTY", starts)
			}
			a.invalidateSessionRestores()
			a.Shutdown(context.Background())
		})
	}
}

func TestStartupRestoreOldCleanupPreservesNewGeneration(t *testing.T) {
	for _, older := range []bool{false, true} {
		t.Run(map[bool]string{false: "old-shutdown", true: "late-startup-cleanup"}[older], func(t *testing.T) {
			a, store, _, root := testApp(t)
			bin := filepath.Join(root, "agy.exe")
			os.WriteFile(bin, []byte("fixture"), 0600)
			id := "01900000-0000-7000-8000-000000000001"
			key := "antigravity:" + id
			p := provider.NewAntigravity(provider.AntigravityConfig{Lookup: func(string) (string, error) { return bin, nil }, HelperExecutable: bin, TempRoot: root, History: appAntigravityHistory{sessions: []provider.Session{{Ref: provider.SessionRef{Provider: provider.Antigravity, ID: id}, Dir: root}}}})
			a.providers = a.newProviderRegistry(false)
			a.providers.Register(p)
			a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(string, string, int, int, []string) (terminal.Pty, error) { return newShutdownPty(), nil })
			a.ctx = context.Background()
			a.lifecycleID = 1
			store.SaveOpen([]string{key})
			old, err := a.BeginSessionRestore("antigravity")
			if err != nil {
				t.Fatal(err)
			}
			release, cleaned := make(chan struct{}), make(chan struct{})
			go func() {
				<-release
				if older {
					a.invalidateSessionRestoreGenerations(2, true, false)
				} else {
					a.cancelAgentLaunches(1)
				}
				close(cleaned)
			}()
			// Publish the next lifecycle while the old owner's cleanup is suspended.
			a.lifecycleMu.Lock()
			a.lifecycleID = 2
			a.lifecycleMu.Unlock()
			fresh, err := a.BeginSessionRestore("antigravity")
			if err != nil {
				t.Fatal(err)
			}
			close(release)
			<-cleaned
			if _, err := a.RestoreSession(old.Token, key); err == nil {
				t.Fatal("old token survived generation change")
			}
			if _, err := a.RestoreSession(fresh.Token, key); err != nil {
				t.Fatal("old cleanup canceled the new lifecycle", err)
			}
			a.EndSessionRestore(fresh.Token)
			a.TermKill(key)
			a.Shutdown(context.Background())
		})
	}
}

func TestStartupRestoreAllowlistSurvivesEarlierProviderPersistence(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "keep-pending", true: "honor-user-close"}[closed], func(t *testing.T) {
			a, store, _, root := testApp(t)
			bin := filepath.Join(root, "agy.exe")
			os.WriteFile(bin, []byte("fixture"), 0600)
			id := "01900000-0000-7000-8000-000000000001"
			key := "antigravity:" + id
			claude := "01900000-0000-7000-8000-000000000002"
			p := provider.NewAntigravity(provider.AntigravityConfig{Lookup: func(string) (string, error) { return bin, nil }, HelperExecutable: bin, TempRoot: root, History: appAntigravityHistory{sessions: []provider.Session{{Ref: provider.SessionRef{Provider: provider.Antigravity, ID: id}, Dir: root}}}})
			a.providers = a.newProviderRegistry(false)
			a.providers.Register(p)
			a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(string, string, int, int, []string) (terminal.Pty, error) { return newShutdownPty(), nil })
			if err := store.SaveOpen([]string{claude, key}); err != nil {
				t.Fatal(err)
			}
			if refs := a.GetOpenSessionRefs(); len(refs) != 2 {
				t.Fatal(refs)
			}
			if closed {
				a.TermKill(key)
			}
			if _, err := a.StartSession(claude, root); err != nil {
				t.Fatal(err)
			}
			batch, err := a.BeginSessionRestore("antigravity")
			if err != nil {
				t.Fatal(err)
			}
			_, err = a.RestoreSession(batch.Token, key)
			if (err != nil) != closed {
				t.Fatalf("earlier provider rewrote startup authorization (closed=%t): %v", closed, err)
			}
			a.EndSessionRestore(batch.Token)
			a.TermKill(claude)
			a.TermKill(key)
			a.Shutdown(context.Background())
		})
	}
}

func TestStartupRestoreRejectsClosedIDEvenIfBatchPublishedFromEarlierSnapshot(t *testing.T) {
	a, store, _, root := testApp(t)
	bin := filepath.Join(root, "agy.exe")
	os.WriteFile(bin, []byte("fixture"), 0600)
	id := "01900000-0000-7000-8000-000000000001"
	key := "antigravity:" + id
	p := provider.NewAntigravity(provider.AntigravityConfig{Lookup: func(string) (string, error) { return bin, nil }, HelperExecutable: bin, TempRoot: root, History: appAntigravityHistory{sessions: []provider.Session{{Ref: provider.SessionRef{Provider: provider.Antigravity, ID: id}, Dir: root}}}})
	a.providers = a.newProviderRegistry(false)
	a.providers.Register(p)
	starts := 0
	a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(string, string, int, int, []string) (terminal.Pty, error) { starts++; return newShutdownPty(), nil })
	store.SaveOpen([]string{key})
	batch, err := a.BeginSessionRestore("antigravity")
	if err != nil {
		t.Fatal(err)
	}
	// Model a batch constructed from the old allowlist but not yet visible to
	// TermKill: closing must remain authoritative when that batch is published.
	a.restoreMu.Lock()
	unpublished := a.restoreBatches[batch.Token]
	delete(a.restoreBatches, batch.Token)
	a.restoreMu.Unlock()
	a.TermKill(key)
	a.restoreMu.Lock()
	a.restoreBatches[batch.Token] = unpublished
	a.restoreMu.Unlock()
	_, err = a.RestoreSession(batch.Token, key)
	a.EndSessionRestore(batch.Token)
	a.TermKill(key)
	a.Shutdown(context.Background())
	if err == nil || starts != 0 {
		t.Fatal("stale batch bypassed the explicit close", err, starts)
	}
}

func TestStartupRestorePendingReferencesSurviveNormalExitWithoutRevivingOpenedTerminals(t *testing.T) {
	for _, mode := range []string{"pending", "closed", "opened-unconfirmed", "opened-query-failed", "old-generation"} {
		t.Run(mode, func(t *testing.T) {
			a, store, _, root := testApp(t)
			bin := filepath.Join(root, "agy.exe")
			os.WriteFile(bin, []byte("fixture"), 0600)
			id := "01900000-0000-7000-8000-000000000001"
			key := "antigravity:" + id
			claude := "01900000-0000-7000-8000-000000000002"
			failed := false
			p := provider.NewAntigravity(provider.AntigravityConfig{Lookup: func(string) (string, error) { return bin, nil }, HelperExecutable: bin, TempRoot: root, History: codexHistoryFunc(func(context.Context) ([]provider.Session, error) {
				if failed {
					return nil, errors.New("synthetic exit query failure")
				}
				return []provider.Session{{Ref: provider.SessionRef{Provider: provider.Antigravity, ID: id}, Dir: root}}, nil
			})})
			a.providers = a.newProviderRegistry(false)
			a.providers.Register(p)
			var snapshot string
			a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(_, _ string, _, _ int, env []string) (terminal.Pty, error) {
				for _, entry := range env {
					if v, ok := strings.CutPrefix(entry, "THEONE_AGY_SNAPSHOT="); ok {
						snapshot = v
					}
				}
				return newShutdownPty(), nil
			})
			store.SaveOpen([]string{claude, key})
			a.GetOpenSessionRefs()
			if _, err := a.StartSession(claude, root); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "closed":
				a.TermKill(key)
			case "opened-unconfirmed", "opened-query-failed":
				batch, err := a.BeginSessionRestore("antigravity")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = a.RestoreSession(batch.Token, key); err != nil {
					t.Fatal(err)
				}
				a.EndSessionRestore(batch.Token)
				if mode == "opened-query-failed" {
					provider.WriteAntigravitySnapshot(strings.NewReader(`{"conversation_id":"`+id+`","agent_state":"idle"}`), snapshot)
					failed = true
				}
			case "old-generation":
				a.lifecycleMu.Lock()
				a.lifecycleID++
				a.ctx = context.Background()
				a.lifecycleMu.Unlock()
			}
			a.Shutdown(context.Background())
			fresh := NewAppWithStore(store)
			refs := fresh.GetOpenSessionRefs()
			want := 1
			if mode == "pending" {
				want = 2
			}
			if len(refs) != want {
				t.Fatalf("normal exit lost pending intent or revived opened/closed terminal: mode=%s refs=%v", mode, refs)
			}
		})
	}
}

func TestStartupRestoreLateCloseDuringFinalShutdownKeepsOtherPendingReference(t *testing.T) {
	a, store, _, root := testApp(t)
	a.providers.Register(provider.NewAntigravity(provider.AntigravityConfig{History: appAntigravityHistory{}}))
	ids := []string{"antigravity:01900000-0000-7000-8000-000000000001", "antigravity:01900000-0000-7000-8000-000000000002"}
	claude := "01900000-0000-7000-8000-000000000003"
	a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(string, string, int, int, []string) (terminal.Pty, error) { return newShutdownPty(), nil })
	store.SaveOpen(append(append([]string{}, ids...), claude))
	a.GetOpenSessionRefs()
	if _, err := a.StartSession(claude, root); err != nil {
		t.Fatal(err)
	}
	a.lifecycleMu.Lock()
	generation := a.lifecycleID
	a.stopping = true
	a.lifecycleID++
	a.ctx = nil
	a.lifecycleMu.Unlock()
	a.TermKill(ids[1])
	a.GetOpenSessionRefs() // A late UI read must not replace the owner's snapshot.
	if err := a.finishTerminals(nil, generation, false); err != nil {
		t.Fatal(err)
	}
	refs := NewAppWithStore(store).GetOpenSessionRefs()
	found := map[string]bool{}
	for _, ref := range refs {
		found[ref.ID] = true
	}
	if len(refs) != 2 || !found[ids[0]] || !found[claude] || found[ids[1]] {
		t.Fatal("late stop-phase access reloaded or revived startup intent", refs)
	}
}
