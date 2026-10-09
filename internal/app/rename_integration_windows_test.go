//go:build windows && integration

package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/gp-alex-chen/claude-session-manager/internal/session"
	"github.com/gp-alex-chen/claude-session-manager/internal/terminal"
)

// The embedded offline SDK only touches a synthetic CLAUDE_CONFIG_DIR; no
// Claude executable, credentials, model query or user history is involved.
func TestClaudeNativeRenameShutdownPersistStartupRestoreIntegration(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node.js integration unavailable")
	}
	a, store, _, _ := testApp(t)
	root := t.TempDir()
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	projects := filepath.Join(home, "projects")
	encoded := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(project, "-")
	dir := filepath.Join(projects, encoded)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	id := "01900000-0000-7000-8000-000000000001"
	file := filepath.Join(dir, id+".jsonl")
	data, err := json.Marshal(map[string]any{"type": "user", "sessionId": id, "cwd": project, "uuid": "fixture-user", "message": map[string]string{"role": "user", "content": "Synthetic only"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	a.sessionCatalog = session.NewCatalog(projects)
	a.terms = terminal.NewManagerWithStart(terminal.Callbacks{}, store.SaveOpen, func(string, string, int, int, []string) (terminal.Pty, error) { return newShutdownPty(), nil })
	if err := store.SetAlias(id, "Stale legacy alias"); err != nil {
		t.Fatal(err)
	}
	if list := a.ListSessions(); len(list) != 1 || list[0].Name == "Stale legacy alias" {
		t.Fatalf("native names masked by aliases: %v", list)
	}
	if _, err := a.StartSession(id, project); err != nil {
		t.Fatal(err)
	}
	name := `新名字 "quoted" C:\work`
	if err := a.RenameSession(id, " "+name+" "); err != nil {
		t.Fatal(err)
	}
	if list := a.ListSessions(); len(list) != 1 || list[0].Name != name {
		t.Fatalf("stale catalog after rename: %v", list)
	}
	a.shutdown(context.Background())
	restarted, _, _, _ := testApp(t)
	restarted.store = store
	restarted.sessionCatalog = session.NewCatalog(projects)
	started := ""
	restarted.startPTYFn = func(token, _, gotDir string) error {
		if gotDir != project {
			t.Fatalf("restore directory=%q", gotDir)
		}
		started = token
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	restarted.startup(ctx)
	defer restarted.shutdown(context.Background())
	for _, key := range restarted.GetOpenSessions() {
		if _, err := restarted.StartSession(key, project); err != nil {
			t.Fatal(err)
		}
	}
	list := restarted.ListSessions()
	if started != id || len(list) != 1 || list[0].Name != name {
		t.Fatalf("restored token=%q list=%v", started, list)
	}
	st, err := store.Load()
	if err != nil || st.Aliases[id] != "Stale legacy alias" {
		t.Fatalf("native rename wrote local aliases: %+v %v", st, err)
	}
}
