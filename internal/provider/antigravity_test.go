package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestAntigravityLaunchIsolationAndCapabilities(t *testing.T) {
	t.Setenv("AGY_CLI_DISABLE_AUTO_UPDATE", "false")
	root := t.TempDir()
	bin := filepath.Join(root, "official install", "agy.exe")
	os.MkdirAll(filepath.Dir(bin), 0700)
	os.WriteFile(bin, []byte("fixture; never executed"), 0600)
	p := NewAntigravity(AntigravityConfig{Lookup: func(string) (string, error) { return "", errors.New("stale PATH") }, Getenv: func(name string) string {
		if name == "LOCALAPPDATA" {
			return root
		}
		return ""
	}, HelperExecutable: bin, TempRoot: root})
	// A missing fallback must fail before allocating runtime resources.
	if _, err := p.PrepareLaunch(context.Background(), OpenRequest{RuntimeToken: "new-antigravity-one"}); err == nil {
		t.Fatal("missing CLI accepted")
	}
	official := filepath.Join(root, "agy", "bin", "agy.exe")
	os.MkdirAll(filepath.Dir(official), 0700)
	os.WriteFile(official, []byte("fixture"), 0600)
	a, err := p.PrepareLaunch(context.Background(), OpenRequest{RuntimeToken: "new-antigravity-one", Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := p.PrepareLaunch(context.Background(), OpenRequest{RuntimeToken: "new-antigravity-two", Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, spec := range []LaunchSpec{a, b} {
		if !slices.Contains(spec.Env, "AGY_CLI_DISABLE_AUTO_UPDATE=true") {
			t.Fatal("managed CLI can spawn its independent updater console")
		}
	}
	if os.Getenv("AGY_CLI_DISABLE_AUTO_UPDATE") != "false" {
		t.Fatal("managed terminal changed parent updater preference")
	}
	if a.Executable != official || a.Identity != nil || a.Args[1] == b.Args[1] {
		t.Fatalf("launch isolation: %#v %#v", a, b)
	}
	data, err := os.ReadFile(a.Args[1])
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if json.Unmarshal(data, &settings) != nil || settings["statusLine"] == nil {
		t.Fatal(string(data))
	}
	r := NewRegistry()
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	d := r.Descriptors()[0]
	if !d.HasStatus || !d.HasHistory || !d.HasQuota || d.HasUsage || d.HasRename || !d.NewSessionRestorable {
		t.Fatalf("overclaimed capabilities: %#v", d)
	}
	if _, err := p.PrepareLaunch(context.Background(), OpenRequest{RuntimeToken: "restore", Ref: &SessionRef{Antigravity, "id"}}); err == nil {
		t.Fatal("unverified restoration accepted")
	}
	a.Close()
	if _, err := os.Stat(filepath.Dir(a.Args[1])); !os.IsNotExist(err) {
		t.Fatal("runtime files retained", err)
	}
	if _, err := os.Stat(b.Args[1]); err != nil {
		t.Fatal("other terminal removed", err)
	}
	customPath := filepath.Join(root, "user-settings.json")
	custom := []byte(`{"statusLine":{"command":"existing-user-status-command"}}`)
	if err := os.WriteFile(customPath, custom, 0600); err != nil {
		t.Fatal(err)
	}
	p.config.UserSettingsPath = customPath
	c, err := p.PrepareLaunch(context.Background(), OpenRequest{RuntimeToken: "new-antigravity-custom"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	settingsData, _ := os.ReadFile(c.Args[1])
	if strings.Contains(string(settingsData), "statusLine") {
		t.Fatal("existing customization overridden", string(settingsData))
	}
	after, _ := os.ReadFile(customPath)
	if string(after) != string(custom) {
		t.Fatal("user settings changed")
	}
}

func TestAntigravitySnapshotHelperFiltersAndReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	for _, state := range []string{"working", "idle"} {
		input := `{"conversation_id":"12345678-abcd-ef01-2345-6789abcdef01","conversation_title":"Synthetic title","agent_state":"` + state + `","email":"private@example.invalid","unexpected":"omit"}`
		if err := WriteAntigravitySnapshot(strings.NewReader(input), path); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), "private") || strings.Contains(string(data), "unexpected") || !strings.Contains(string(data), state) {
			t.Fatal(string(data))
		}
	}
	before, _ := os.ReadFile(path)
	for _, input := range []string{`null`, `{`, strings.Repeat("x", antigravitySnapshotLimit+1), `{"agent_state":"idle"} garbage`} {
		if err := WriteAntigravitySnapshot(strings.NewReader(input), path); err == nil {
			t.Fatal("accepted invalid payload")
		}
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("invalid callback replaced last good snapshot")
	}
}

func TestAntigravitySnapshotReaderSkipsInFlightCommitAndObservesFinalState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := WriteAntigravitySnapshot(strings.NewReader(`{"agent_state":"working"}`), path); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockAntigravitySnapshot(path, true)
	if err != nil {
		t.Fatal(err)
	}
	p := NewAntigravity(AntigravityConfig{})
	r := &antigravityRuntime{snapshotPath: path, info: antigravityAgent("new-antigravity-test", antigravitySnapshot{AgentState: "working"})}
	p.runtimes["new-antigravity-test"] = r
	p.refresh("new-antigravity-test", r)
	if r.info.State != "working" {
		t.Fatal("contended reader overwrote last state")
	}
	unlock()
	if err := WriteAntigravitySnapshot(strings.NewReader(`{"agent_state":"idle"}`), path); err != nil {
		t.Fatal(err)
	}
	p.refresh("new-antigravity-test", r)
	if r.info.State != "idle" {
		t.Fatal("final snapshot not observed")
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "idle") {
		t.Fatal(string(data), err)
	}
}

func TestAntigravityStatusMappingDoesNotInferSuccess(t *testing.T) {
	for _, tc := range []struct {
		native, state, status string
		approval              bool
	}{
		{"working", "working", "busy", false}, {"tool_use", "working", "busy", false}, {"idle", "idle", "idle", false},
		{"error", "failed", "error", false}, {"reviewing", "unknown", "unknown", false},
		{"working", "blocked", "waiting", true}, {"initializing", "unknown", "unknown", false},
	} {
		got := antigravityAgent("new-antigravity-test", antigravitySnapshot{AgentState: tc.native, ConfirmationPending: tc.approval, ConversationTitle: "Title"})
		if got.State != tc.state || got.Status != tc.status || got.Provider != "antigravity" || got.SessionID != "new-antigravity-test" || got.Restorable || got.TurnStatus != "" {
			t.Fatalf("%s: %#v", tc.native, got)
		}
	}
}

func TestAntigravityRefreshSignalsOnlyChangedTerminalAndRetainsQuietState(t *testing.T) {
	root := t.TempDir()
	var updates atomic.Int64
	p := NewAntigravity(AntigravityConfig{OnChange: func() { updates.Add(1) }})
	for _, token := range []string{"new-antigravity-one", "new-antigravity-two"} {
		p.runtimes[token] = &antigravityRuntime{dir: root, snapshotPath: filepath.Join(root, token+".json"), info: antigravityAgent(token, antigravitySnapshot{})}
	}
	path := p.runtimes["new-antigravity-one"].snapshotPath
	if err := WriteAntigravitySnapshot(strings.NewReader(`{"agent_state":"working","conversation_title":"Only one"}`), path); err != nil {
		t.Fatal(err)
	}
	list, _ := p.Status(context.Background())
	if len(list) != 2 || list[0].State != "working" || list[1].State != "unknown" || updates.Load() != 1 {
		t.Fatalf("cross-terminal state or missing signal: %#v %d", list, updates.Load())
	}
	list, _ = p.Status(context.Background())
	if list[0].State != "working" || updates.Load() != 1 {
		t.Fatal("quiet snapshot treated as stale or repeatedly notified")
	}
	if err := WriteAntigravitySnapshot(strings.NewReader(`{"agent_state":"idle"}`), path); err != nil {
		t.Fatal(err)
	}
	list, _ = p.Status(context.Background())
	if list[0].State != "idle" || updates.Load() != 2 {
		t.Fatal("last idle state lost")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	list, _ = p.Status(context.Background())
	if list[0].State != "unknown" || updates.Load() != 3 {
		t.Fatal("explicit missing snapshot retained stale work")
	}
}

func TestAntigravityRefreshRecoversWhenLockFileBecomesAvailable(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "snapshot.json")
	if err := WriteAntigravitySnapshot(strings.NewReader(`{"agent_state":"working"}`), path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path + ".lock"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".lock", 0700); err != nil {
		t.Fatal(err)
	}
	p := NewAntigravity(AntigravityConfig{})
	token := "new-antigravity-recover-lock"
	p.runtimes[token] = &antigravityRuntime{snapshotPath: path, info: antigravityAgent(token, antigravitySnapshot{})}
	list, _ := p.Status(context.Background())
	if list[0].State != "unknown" {
		t.Fatal("read without required lock")
	}
	if err := os.Remove(path + ".lock"); err != nil {
		t.Fatal(err)
	}
	list, _ = p.Status(context.Background())
	if list[0].State != "working" {
		t.Fatal("unchanged snapshot did not recover after lock error")
	}
}

type antigravityHistoryFunc func(context.Context) ([]Session, error)

func (f antigravityHistoryFunc) ListSessions(ctx context.Context) ([]Session, error) { return f(ctx) }

func TestAntigravityRestorationConfirmsFirstIdentityAndRejectsAmbiguousSwitches(t *testing.T) {
	for _, nextID := range []string{"", "01900000-0000-7000-8000-000000000002"} {
		t.Run("switch-"+nextID, func(t *testing.T) {
			id := "01900000-0000-7000-8000-000000000001"
			token, dir := "new-antigravity-restore", `C:\synthetic`
			failed := false
			reads := 0
			p := NewAntigravity(AntigravityConfig{History: antigravityHistoryFunc(func(context.Context) ([]Session, error) {
				reads++
				if failed {
					return nil, errors.New("synthetic catalog failure")
				}
				return []Session{{Ref: SessionRef{Antigravity, id}, Dir: dir}}, nil
			})})
			source, ok := any(p).(interface {
				RestoreReferences(context.Context) (map[string]string, error)
			})
			if !ok {
				t.Fatal("Antigravity has no confirmed restore reference source")
			}
			path := filepath.Join(t.TempDir(), "snapshot.json")
			p.runtimes[token] = &antigravityRuntime{dir: dir, snapshotPath: path}
			write := func(value string) {
				t.Helper()
				if err := WriteAntigravitySnapshot(strings.NewReader(`{"conversation_id":"`+value+`","agent_state":"idle"}`), path); err != nil {
					t.Fatal(err)
				}
			}
			refs, err := source.RestoreReferences(context.Background())
			if err != nil || len(refs) != 0 || reads != 0 {
				t.Fatal("unobserved identity queried or persisted", refs, err, reads)
			}
			write(id)
			refs, err = source.RestoreReferences(context.Background())
			if err != nil || refs[token] != "antigravity:"+id {
				t.Fatal("durable native identity was not confirmed", refs, err)
			}
			failed = true
			if refs, err = source.RestoreReferences(context.Background()); err == nil || len(refs) != 0 {
				t.Fatal("cached history authorized restore", refs, err)
			}
			failed = false
			write(nextID)
			refs, _ = source.RestoreReferences(context.Background())
			if len(refs) != 0 {
				t.Fatal("ambiguous identity remained recoverable", refs)
			}
			write(id)
			refs, _ = source.RestoreReferences(context.Background())
			if len(refs) != 0 {
				t.Fatal("late original callback revived restoration", refs)
			}
		})
	}
}

func TestAntigravityPowerShellHostSkipsProfiles(t *testing.T) {
	for _, args := range [][]string{{"--settings", "synthetic"}, {"--conversation", "01900000-0000-7000-8000-000000000001", "--settings", "synthetic"}} {
		command, err := AntigravityShellCommand("pwsh", LaunchSpec{Command: "agy", Args: args})
		if err != nil || !strings.HasPrefix(command, `pwsh -NoLogo -NoProfile -NoExit -Command `) {
			t.Fatalf("agent startup may run profile commands outside the embedded terminal: %q %v", command, err)
		}
	}
}

func TestAntigravityRestoreQueryCannotReviveClosedOrReplacedRuntime(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprint(replace), func(t *testing.T) {
			id, token, dir := "01900000-0000-7000-8000-000000000001", "new-antigravity-old", `C:\synthetic`
			path := filepath.Join(t.TempDir(), "snapshot.json")
			if err := WriteAntigravitySnapshot(strings.NewReader(`{"conversation_id":"`+id+`"}`), path); err != nil {
				t.Fatal(err)
			}
			p := NewAntigravity(AntigravityConfig{})
			p.runtimes[token] = &antigravityRuntime{dir: dir, snapshotPath: path}
			p.config.History = antigravityHistoryFunc(func(context.Context) ([]Session, error) {
				p.mu.Lock()
				delete(p.runtimes, token)
				if replace {
					p.runtimes[token] = &antigravityRuntime{dir: dir, snapshotPath: path}
				}
				p.mu.Unlock()
				return []Session{{Ref: SessionRef{Antigravity, id}, Dir: dir}}, nil
			})
			refs, err := p.RestoreReferences(context.Background())
			if err != nil || len(refs) != 0 {
				t.Fatal("stale runtime became recoverable", refs, err)
			}
		})
	}
}

func TestAntigravityHistoryCacheStartsAfterSlowQueryCompletes(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure-retains-last-good"}[failed], func(t *testing.T) {
			now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			reads := 0
			lastGood := []Session{{Ref: SessionRef{Antigravity, "01900000-0000-7000-8000-000000000001"}, Dir: `C:\synthetic`, Name: "Last good title"}}
			queryErr := errors.New("synthetic catalog failure")
			p := NewAntigravity(AntigravityConfig{History: antigravityHistoryFunc(func(context.Context) ([]Session, error) {
				reads++
				now = now.Add(12 * time.Second)
				if failed {
					return nil, queryErr
				}
				return lastGood, nil
			})})
			p.now = func() time.Time { return now }
			p.historyCache = lastGood
			check := func() {
				t.Helper()
				list, err := p.ListSessions(context.Background())
				if len(list) != 1 || list[0].Name != lastGood[0].Name || (failed && !errors.Is(err, queryErr)) || (!failed && err != nil) {
					t.Fatal("cache lost history/error semantics", list, err)
				}
			}
			check()
			completed := now
			now = completed.Add(9 * time.Second)
			check()
			if reads != 1 {
				t.Fatal("query duration consumed cache lifetime", reads)
			}
			now = completed.Add(10 * time.Second)
			check()
			if reads != 2 {
				t.Fatal("expired cache was not refreshed", reads)
			}
			_, _ = p.readHistory(context.Background(), true, "")
			if reads != 3 {
				t.Fatal("force query used cached result", reads)
			}
		})
	}
}

func TestAntigravityKnownHistoryLaunchRequiresFreshExactIdentityAndDirectory(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "agy.exe")
	if err := os.WriteFile(bin, []byte("fixture; never executed"), 0600); err != nil {
		t.Fatal(err)
	}
	id := "01900000-0000-7000-8000-000000000001"
	key := "antigravity:" + id
	reads := 0
	fail := false
	p := NewAntigravity(AntigravityConfig{Lookup: func(string) (string, error) { return bin, nil }, HelperExecutable: bin, TempRoot: root,
		History: antigravityHistoryFunc(func(context.Context) ([]Session, error) {
			reads++
			if fail {
				return nil, errors.New("catalog unavailable")
			}
			return []Session{{Ref: SessionRef{Antigravity, id}, Dir: root, Name: "Saved native title"}}, nil
		})})
	if list, err := p.ListSessions(context.Background()); err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	if _, err := p.ListSessions(context.Background()); err != nil || reads != 1 {
		t.Fatal("history cache not reused", reads, err)
	}
	spec, err := p.PrepareLaunch(context.Background(), OpenRequest{RuntimeToken: key, Dir: root, Ref: &SessionRef{Antigravity, id}})
	if err != nil {
		t.Fatal(err)
	}
	defer spec.Close()
	if reads != 2 || len(spec.Args) != 4 || spec.Args[0] != "--conversation" || spec.Args[1] != id || spec.Identity != nil {
		t.Fatalf("resume not preflighted: %#v reads=%d", spec, reads)
	}
	wrong := filepath.Join(root, "other")
	if err := os.Mkdir(wrong, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PrepareLaunch(context.Background(), OpenRequest{RuntimeToken: key, Dir: wrong, Ref: &SessionRef{Antigravity, id}}); err == nil {
		t.Fatal("cross-directory resume accepted")
	}
	missing := "01900000-0000-7000-8000-000000000002"
	if _, err := p.PrepareLaunch(context.Background(), OpenRequest{RuntimeToken: "antigravity:" + missing, Dir: root, Ref: &SessionRef{Antigravity, missing}}); err == nil {
		t.Fatal("missing native ID treated as new")
	}
	fail = true
	if _, err := p.PrepareLaunch(context.Background(), OpenRequest{RuntimeToken: key, Dir: root, Ref: &SessionRef{Antigravity, id}}); err == nil {
		t.Fatal("stale cached history authorized resume")
	}
	p.now = func() time.Time { return time.Now().Add(time.Hour) }
	if list, err := p.ListSessions(context.Background()); err == nil || len(list) != 1 {
		t.Fatal("failed catalog erased last good history", list, err)
	}
}

func TestAntigravityKnownRuntimeNeverReconfirmsAfterAmbiguousNativeSwitch(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "snapshot.json")
	a, b := "01900000-0000-7000-8000-000000000001", "01900000-0000-7000-8000-000000000002"
	p := NewAntigravity(AntigravityConfig{})
	r := &antigravityRuntime{expectedID: a, snapshotPath: path, info: antigravityAgent("antigravity:"+a, antigravitySnapshot{})}
	token := "antigravity:" + a
	p.runtimes[token] = r
	for i, id := range []string{a, b, a} {
		payload := `{"conversation_id":"` + id + `","conversation_title":"Native title","agent_state":"idle"}`
		if err := WriteAntigravitySnapshot(strings.NewReader(payload), path); err != nil {
			t.Fatal(err)
		}
		list, _ := p.Status(context.Background())
		if list[0].Restorable {
			t.Fatal("unordered callback enabled restoration")
		}
		if i == 0 && list[0].State != "idle" {
			t.Fatal("expected native identity not confirmed")
		}
		if i > 0 && (list[0].State != "unknown" || list[0].Name != "") {
			t.Fatal("late callback restored ambiguous identity", list[0])
		}
	}
}

func TestAntigravitySuccessfulLaunchOutlivesCanceledPreparationContext(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "agy.exe")
	if err := os.WriteFile(bin, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	changed := make(chan struct{}, 1)
	p := NewAntigravity(AntigravityConfig{Lookup: func(string) (string, error) { return bin, nil }, HelperExecutable: bin, TempRoot: root, OnChange: func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	}})
	preparation, cancel := context.WithCancel(context.Background())
	spec, err := p.PrepareLaunch(preparation, OpenRequest{RuntimeToken: "new-antigravity-context", Dir: root, RuntimeContext: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	defer spec.Close()
	cancel()
	var path string
	for _, entry := range spec.Env {
		if value, ok := strings.CutPrefix(entry, "THEONE_AGY_SNAPSHOT="); ok {
			path = value
		}
	}
	if err := WriteAntigravitySnapshot(strings.NewReader(`{"agent_state":"working"}`), path); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case <-time.After(3 * time.Second):
		t.Fatal("successful runtime worker canceled with metadata request")
	}
	list, _ := p.Status(context.Background())
	if len(list) != 1 || list[0].State != "working" {
		t.Fatal(list)
	}
}

func TestAntigravityExitCatalogBudgetStartsAfterQueuedQuery(t *testing.T) {
	for _, restoring := range []bool{false, true} {
		t.Run(fmt.Sprint(restoring), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				const id = "01900000-0000-7000-8000-000000000001"
				token, dir := "new-antigravity-queued", `C:\synthetic`
				started := make(chan struct{})
				calls := 0
				var queryBudget time.Duration
				p := NewAntigravity(AntigravityConfig{History: antigravityHistoryFunc(func(ctx context.Context) ([]Session, error) {
					calls++
					if calls == 1 {
						close(started)
					} else {
						deadline, _ := ctx.Deadline()
						queryBudget = time.Until(deadline)
					}
					// synctest advances this timer using its fake clock; no wall-clock wait.
					select {
					case <-time.After(14 * time.Second):
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					return []Session{{Ref: SessionRef{Antigravity, id}, Dir: dir}}, nil
				})})
				path := filepath.Join(t.TempDir(), "snapshot.json")
				if err := WriteAntigravitySnapshot(strings.NewReader(`{"conversation_id":"`+id+`","agent_state":"idle"}`), path); err != nil {
					t.Fatal(err)
				}
				p.runtimes[token] = &antigravityRuntime{dir: dir, snapshotPath: path}
				inflight := make(chan error, 1)
				go func() { _, err := p.ListSessions(context.Background()); inflight <- err }()
				<-started
				begin := time.Now()
				if restoring {
					refs, err := p.RestoreReferences(context.Background())
					if inflightErr := <-inflight; inflightErr != nil {
						t.Fatal(inflightErr)
					}
					if err != nil || refs[token] != "antigravity:"+id {
						t.Fatalf("queued exit lost native reference: %v %v", refs, err)
					}
					if queryBudget != 15*time.Second || time.Since(begin) != 28*time.Second {
						t.Fatalf("fresh exit query lost transport budget: %s total %s", queryBudget, time.Since(begin))
					}
				} else {
					_, err := p.readHistory(context.Background(), true, "")
					if !errors.Is(err, context.DeadlineExceeded) || time.Since(begin) != 15*time.Second {
						t.Fatalf("ordinary UI query exceeded its total limit: %s %v", time.Since(begin), err)
					}
				}
				if !restoring {
					if err := <-inflight; err != nil {
						t.Fatal(err)
					}
				}
				if calls != 2 {
					t.Fatalf("expected in-flight and fresh catalog reads, got %d", calls)
				}
			})
		})
	}
}
