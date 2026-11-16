//go:build windows && integration

package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/UserExistsError/conpty"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gp-alex-chen/claude-session-manager/internal/provider"
	"github.com/gp-alex-chen/claude-session-manager/internal/state"
	"github.com/gp-alex-chen/claude-session-manager/internal/terminal"
)

// Explicit opt-in targets only a dedicated native test conversation. Terminal
// output is drained for VT negotiation and never logged or saved.
func TestAntigravityNormalExitReopensNativeConversationIntegration(t *testing.T) {
	bin, dir, id, host := os.Getenv("THEONE_AGY_LIVE_CLI"), os.Getenv("THEONE_AGY_LIVE_CWD"), os.Getenv("THEONE_AGY_LIVE_SESSION"), os.Getenv("THEONE_ANTIGRAVITY_TEST_HOST")
	if bin == "" || dir == "" || id == "" || host == "" {
		t.Skip("set explicit dedicated Antigravity CLI/CWD/SESSION/HOST fixtures")
	}
	key, err := provider.EncodeKey(provider.SessionRef{Provider: provider.Antigravity, ID: id})
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(t.TempDir())
	if err := store.SaveOpen([]string{key}); err != nil {
		t.Fatal(err)
	}
	for cycle := 0; cycle < 2; cycle++ {
		a := NewAppWithStore(store)
		a.debugLog = func(message string) { t.Log(message) }
		a.providers = a.newProviderRegistry(false)
		changed := make(chan struct{}, 1)
		p := provider.NewAntigravity(provider.AntigravityConfig{Lookup: func(string) (string, error) { return bin, nil }, HelperExecutable: host, TempRoot: t.TempDir(), OnChange: func() {
			select {
			case changed <- struct{}{}:
			default:
			}
		}})
		if err := a.providers.Register(p); err != nil {
			t.Fatal(err)
		}
		callbacks := terminal.Callbacks{Data: func(token, encoded string) {
			data, _ := base64.StdEncoding.DecodeString(encoded)
			for query, reply := range map[string]string{"\x1b[6n": "\x1b[1;1R", "\x1b[c": "\x1b[?1;2c", "\x1b]11;": "\x1b]11;rgb:1111/1111/1111\x1b\\"} {
				if strings.Contains(string(data), query) {
					a.TermWrite(token, base64.StdEncoding.EncodeToString([]byte(reply)))
				}
			}
		}}
		a.terms = terminal.NewManagerWithStart(callbacks, store.SaveOpen, func(command, workDir string, cols, rows int, env []string) (terminal.Pty, error) {
			for _, entry := range env {
				if path, ok := strings.CutPrefix(entry, "THEONE_AGY_SETTINGS="); ok {
					data, err := os.ReadFile(path)
					if err != nil {
						return nil, err
					}
					var settings map[string]any
					if err := json.Unmarshal(data, &settings); err != nil {
						return nil, err
					}
					settings["trustedWorkspaces"] = []string{dir}
					settings["altScreenMode"] = "always"
					settings["showTips"] = false
					data, err = json.Marshal(settings)
					if err != nil {
						return nil, err
					}
					if err = os.WriteFile(path, data, 0600); err != nil {
						return nil, err
					}
				}
			}
			command = strings.TrimSuffix(command, `"`) + ` -i "/usage""`
			return conpty.Start(command, conpty.ConPtyWorkDir(workDir), conpty.ConPtyDimensions(cols, rows), conpty.ConPtyEnv(env))
		})
		if cycle == 1 {
			refs := a.GetOpenSessionRefs()
			if len(refs) != 1 || refs[0].ID != key {
				t.Fatal("fresh app did not load the saved native reference")
			}
		}
		batch, err := a.BeginSessionRestore("antigravity")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.RestoreSession(batch.Token, key); err != nil {
			a.EndSessionRestore(batch.Token)
			t.Fatal(err)
		}
		// Ending the metadata lease must not stop the successfully launched TUI.
		a.EndSessionRestore(batch.Token)
		deadline := time.NewTimer(30 * time.Second)
		confirmed := false
		for !confirmed {
			list, _ := p.Status(context.Background())
			for _, info := range list {
				if info.SessionID == id && info.State != "unknown" {
					confirmed = true
				}
			}
			if confirmed {
				break
			}
			select {
			case <-changed:
			case <-deadline.C:
				a.TermKill(key)
				a.Shutdown(context.Background())
				t.Fatal("native TUI did not confirm the requested conversation")
			}
		}
		deadline.Stop()
		t.Logf("cycle %d: native TUI confirmed the exact dedicated conversation", cycle+1)
		if cycle == 1 {
			a.TermKill(key)
		}
		a.Shutdown(context.Background())
	}
	if refs := NewAppWithStore(store).GetOpenSessionRefs(); len(refs) != 0 {
		t.Fatal("explicitly closed native conversation was restored again")
	}
}
