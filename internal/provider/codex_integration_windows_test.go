//go:build windows && integration

package provider

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UserExistsError/conpty"
)

// This test uses the installed CLI but only synthetic transcripts in an
// isolated CODEX_HOME. It never signs in, sends a turn, or reads user history.
func TestCodexCLIHistoryAndResumeIntegration(t *testing.T) {
	bin, _, project, id, env := codexIntegrationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := &CodexRPC{Open: func(ctx context.Context) (io.ReadWriteCloser, error) { return openCodexRPCProcess(ctx, bin, env) }}
	// Seed the native index by opening the synthetic legacy rollout through
	// the official resume API, without sending a model turn.
	conn, err := c.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	r := newRPCSession(ctx, conn)
	var result map[string]any
	if err := r.call("initialize", map[string]any{"clientInfo": map[string]string{"name": "session_manager_integration", "version": "test"}}, &result); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("{\"method\":\"initialized\"}\n")); err != nil {
		t.Fatal(err)
	}
	var resumed struct{ Thread struct{ ID string } }
	if err := r.call("thread/resume", map[string]string{"threadId": id}, &resumed); err != nil {
		t.Fatal(err)
	}
	if resumed.Thread.ID != id {
		t.Fatalf("resumed wrong thread: %#v", resumed)
	}
	conn.Close()
	// Query from a fresh process, as the application's read-only history does.
	list, err := c.ListSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Ref.ID != id || list[0].Dir != project || list[0].Name != "SYNTHETIC_HISTORY_ONLY" {
		t.Fatalf("isolated history: %#v", list)
	}
}

func codexIntegrationFixture(t *testing.T) (bin, home, project, id string, env []string) {
	t.Helper()
	bin, err := ResolveCodexExecutable(exec.LookPath)
	if err != nil {
		t.Skip("Codex CLI integration unavailable: " + err.Error())
	}
	version, err := exec.Command(bin, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(version)) != "codex-cli 0.161.0" {
		t.Skip("synthetic rollout fixture is versioned for codex-cli 0.161.0; actual " + strings.TrimSpace(string(version)))
	}
	root := t.TempDir()
	home, project = filepath.Join(root, "home"), filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	id = "01900000-0000-7000-8000-000000000001"
	path := filepath.Join(home, "sessions", "2026", "10", "08", "rollout-2026-10-08T00-00-00-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	records := []map[string]any{
		{"timestamp": "2026-10-08T00:00:00Z", "type": "session_meta", "payload": map[string]any{"id": id, "timestamp": "2026-10-08T00:00:00Z", "cwd": project, "originator": "codex_cli_rs", "cli_version": "0.161.0", "source": "cli", "model_provider": "openai"}},
		{"timestamp": "2026-10-08T00:00:01Z", "type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []map[string]string{{"type": "input_text", "text": "SYNTHETIC_HISTORY_ONLY"}}}},
		{"timestamp": "2026-10-08T00:00:01Z", "type": "event_msg", "payload": map[string]any{"type": "user_message", "message": "SYNTHETIC_HISTORY_ONLY", "images": []string{}}},
	}
	var data []byte
	for _, record := range records {
		b, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		data = append(append(data, b...), '\n')
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	env = isolatedCodexEnvironment(home)
	return bin, home, project, id, env
}

func isolatedCodexEnvironment(home string) []string {
	var env []string
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		key := strings.ToUpper(name)
		if strings.HasPrefix(key, "OPENAI_") || strings.HasPrefix(key, "CODEX_") || key == "ACCESS_TOKEN" || key == "RUST_LOG" || key == "TERM" || key == "NO_COLOR" {
			continue
		}
		env = append(env, value)
	}
	return append(env, "CODEX_HOME="+home, "TERM=xterm-256color")
}

func TestCodexNativeRenamePersistsAcrossFreshServersIntegration(t *testing.T) {
	bin, home, project, id, env := codexIntegrationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := &CodexRPC{Open: func(ctx context.Context) (io.ReadWriteCloser, error) { return openCodexRPCProcess(ctx, bin, env) }}
	files, err := filepath.Glob(filepath.Join(home, "sessions", "*", "*", "*", "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("fixture files=%v %v", files, err)
	}
	before, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	name := `原生 "名称" C:\project & $(literal)`
	if err := c.Rename(ctx, id, name); err != nil {
		t.Fatal(err)
	}
	list, err := c.ListSessions(ctx)
	if err != nil || len(list) != 1 || list[0].Name != name || list[0].Ref.ID != id || list[0].Dir != project {
		t.Fatalf("fresh server history=%v %v", list, err)
	}
	after, err := os.ReadFile(files[0])
	if err != nil || string(before) != string(after) {
		t.Fatalf("rename modified model transcript: %v", err)
	}
}

func TestCodexConPTYStartupIntegration(t *testing.T) {
	bin, err := ResolveCodexExecutable(exec.LookPath)
	if err != nil {
		t.Skip("Codex CLI integration unavailable: " + err.Error())
	}
	root := t.TempDir()
	env := isolatedCodexEnvironment(root)
	p, err := conpty.Start(`"`+bin+`" --no-daemon`, conpty.ConPtyWorkDir(root), conpty.ConPtyDimensions(100, 30), conpty.ConPtyEnv(env))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	output := make(chan string, 1)
	go func() { buf := make([]byte, 8192); n, _ := p.Read(buf); output <- string(buf[:n]) }()
	select {
	case text := <-output:
		if strings.TrimSpace(text) == "" {
			t.Fatal("Codex produced no TUI output")
		}
		if strings.Contains(text, "unexpected argument") {
			t.Fatal(text)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Codex ConPTY startup timed out")
	}
}
