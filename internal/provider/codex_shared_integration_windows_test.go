//go:build windows && integration

package provider

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func initializeSharedFixture(t *testing.T, ctx context.Context, conn io.ReadWriteCloser) *rpcSession {
	t.Helper()
	rpc := newRPCSession(ctx, conn)
	var result map[string]any
	if err := rpc.call("initialize", map[string]any{"clientInfo": map[string]string{"name": "theone_shared_fixture", "version": "test"}}, &result); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("{\"method\":\"initialized\"}\n")); err != nil {
		t.Fatal(err)
	}
	return rpc
}

func TestCodexSharedWebSocketConnectionsIntegration(t *testing.T) {
	bin, home, dir, _, env := codexIntegrationFixture(t)
	pa, pb := filepath.Join(dir, "project A"), filepath.Join(dir, "project B")
	for i, path := range []string{pa, pb} {
		if err := os.MkdirAll(filepath.Join(path, ".codex"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, ".codex", "config.toml"), []byte(fmt.Sprintf("model=\"fixture-model-%d\"\n", i)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	config := fmt.Sprintf("[features]\nplugins=false\nremote_control=false\n[projects.%q]\ntrust_level=\"trusted\"\n[projects.%q]\ntrust_level=\"trusted\"\n", pa, pb)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"OPENAI_API_KEY":"TEST_NOT_REAL_NO_MODEL_TURN"}`), 0600); err != nil {
		t.Fatal(err)
	}
	owner := newSharedCodexOwner(func(ctx context.Context, generation uint64) (*sharedCodexInstance, error) {
		return startSharedCodexProcess(ctx, bin, home, env, nil, generation, nil)
	})
	defer owner.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	first, a, err := owner.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	second, b, err := owner.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if first.pid != second.pid || first.generation != second.generation {
		t.Fatal("connections did not share a process")
	}
	ra, rb := initializeSharedFixture(t, ctx, a), initializeSharedFixture(t, ctx, b)
	var ta, tb struct {
		Thread struct{ ID, Cwd string }
		Model  string
	}
	if err := ra.call("thread/start", map[string]any{"cwd": pa}, &ta); err != nil {
		t.Fatal(err)
	}
	if err := rb.call("thread/start", map[string]any{"cwd": pb}, &tb); err != nil {
		t.Fatal(err)
	}
	if ta.Thread.ID == tb.Thread.ID || ta.Thread.Cwd != pa || tb.Thread.Cwd != pb {
		t.Fatal(ta, tb)
	}
	if ta.Model != "fixture-model-0" || tb.Model != "fixture-model-1" {
		t.Fatalf("project model configuration lost: A=%s B=%s", ta.Model, tb.Model)
	}
	a.Close()
	var loaded struct{ Data []string }
	if err := rb.call("thread/loaded/list", map[string]any{}, &loaded); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range loaded.Data {
		if id == tb.Thread.ID {
			found = true
		}
	}
	if !found || !first.alive() {
		t.Fatal("closing A stopped B or shared process", loaded)
	}
	_, metadata, err := owner.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rm := initializeSharedFixture(t, ctx, metadata)
	var summary map[string]any
	if err := rm.call("thread/read", map[string]any{"threadId": tb.Thread.ID}, &summary); err != nil {
		t.Fatal(err)
	}
	metadata.Close()
	if err := rb.call("thread/loaded/list", map[string]any{}, &loaded); err != nil {
		t.Fatal("metadata close stopped terminal", err)
	}
	owner.Close()
	if first.alive() {
		t.Fatal("owner shutdown left server alive")
	}
	owner.Begin()
	restarted, c, err := owner.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if restarted.generation <= first.generation {
		t.Fatal("restart reused old generation")
	}
	rc := initializeSharedFixture(t, ctx, c)
	if err := rc.call("thread/start", map[string]any{"cwd": pa}, &ta); err != nil {
		t.Fatal(err)
	}
	process, err := os.FindProcess(restarted.pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-restarted.done:
	case <-ctx.Done():
		t.Fatal("crashed server cleanup timed out")
	}
	fresh, d, err := owner.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if fresh.generation <= restarted.generation {
		t.Fatal("crash reused old generation")
	}
	rd := initializeSharedFixture(t, ctx, d)
	if err := rd.call("thread/start", map[string]any{"cwd": pb}, &tb); err != nil {
		t.Fatal("fresh connection after crash failed", err)
	}
}
