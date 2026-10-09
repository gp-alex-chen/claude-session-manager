package app

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gp-alex-chen/claude-session-manager/internal/state"
	"github.com/gp-alex-chen/claude-session-manager/internal/terminal"
)

func TestLocalAppDataShutdownPersistStartupRestore(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	dir := state.DefaultDir()
	store := state.NewStore(dir)
	a := NewAppWithStore(store)
	a.providers = a.newProviderRegistry(false)
	a.debugLog = func(string) {}
	a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen,
		func(string, string, int, int, []string) (terminal.Pty, error) { return newShutdownPty(), nil })
	if err := store.SetAlias("restored", "Friendly"); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "project")
	if err := store.AddProject(project); err != nil {
		t.Fatal(err)
	}
	if err := store.SetProjectFavorite(project, true); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"ui-theme": "dark", "term-theme": "dracula", "term-font-size": "19", "terminal-layout-mode": "grid-2x2", "session-provider-filter": `["claude"]`} {
		if err := a.SetPreference(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.terms.Start("restored", "cmd", project); err != nil {
		t.Fatal(err)
	}
	a.shutdown(context.Background())
	// A fresh process/store uses the fixed path, independently of the executable.
	restarted := NewAppWithStore(state.NewStore(state.DefaultDir()))
	restarted.providers = restarted.newProviderRegistry(false)
	restarted.debugLog = func(string) {}
	started := []string{}
	restarted.startPTYFn = func(token, _, gotDir string) error {
		if gotDir != project {
			t.Fatalf("restore directory=%q", gotDir)
		}
		started = append(started, token)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	restarted.startup(ctx)
	defer restarted.shutdown(context.Background())
	values, err := restarted.LoadPreferences()
	if err != nil || values["ui-theme"] != "dark" || values["term-theme"] != "dracula" || values["term-font-size"] != "19" || values["terminal-layout-mode"] != "grid-2x2" {
		t.Fatalf("preferences=%v %v", values, err)
	}
	if values["session-provider-filter"] != `["claude"]` {
		t.Fatalf("lost provider selection: %v", values)
	}
	if got := restarted.ListProjectFavorites(); !reflect.DeepEqual(got, []string{project}) {
		t.Fatalf("favorites=%v", got)
	}
	st, err := restarted.store.Load()
	if err != nil || st.Aliases["restored"] != "Friendly" {
		t.Fatalf("session state=%+v %v", st, err)
	}
	for _, id := range restarted.GetOpenSessions() {
		if _, err := restarted.StartSession(id, project); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(started, []string{"restored"}) {
		t.Fatalf("restored=%v", started)
	}
}
