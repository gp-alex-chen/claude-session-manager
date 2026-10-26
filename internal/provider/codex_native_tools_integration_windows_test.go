//go:build windows && integration

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func initializeNativeToolsFixture(t *testing.T, ctx context.Context, conn io.ReadWriteCloser) *rpcSession {
	t.Helper()
	rpc := newRPCSession(ctx, conn)
	var result any
	if err := rpc.call("initialize", map[string]any{"clientInfo": map[string]string{"name": "theone_native_tools_fixture", "version": "test"}, "capabilities": map[string]any{"experimentalApi": true}}, &result); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("{\"method\":\"initialized\"}\n")); err != nil {
		t.Fatal(err)
	}
	return rpc
}

func waitNativeFixtureTurn(t *testing.T, rpc *rpcSession) {
	t.Helper()
	for {
		select {
		case read, ok := <-rpc.reads:
			if !ok || read.err != nil {
				t.Fatalf("native turn failed: %v", read.err)
			}
			if read.msg.Method == "turn/completed" {
				return
			}
		case <-rpc.ctx.Done():
			t.Fatal(rpc.ctx.Err())
		}
	}
}

func TestCodexNativeToolsCatalogAndLegacyResumeIntegration(t *testing.T) {
	catalogs := make(chan json.RawMessage, 8)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct{ Tools json.RawMessage }
		json.NewDecoder(r.Body).Decode(&request)
		catalogs <- request.Tools
		emitSharedFixtureResponse(w, map[string]any{"type": "message", "id": "msg_native_fixture", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "native fixture done", "annotations": []any{}}}})
	}))
	defer model.Close()
	bin, home, dir, oldID, env := codexIntegrationFixture(t)
	config := fmt.Sprintf("model=\"test-model\"\nmodel_provider=\"local_fixture\"\n[model_providers.local_fixture]\nname=\"Local fixture\"\nbase_url=%q\nwire_api=\"responses\"\nrequires_openai_auth=false\nsupports_websockets=false\n[features]\nplugins=false\nremote_control=false\n", model.URL+"/v1")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	instance, err := startSharedCodexProcess(ctx, bin, home, env, nil, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.close()
	conn, err := instance.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rpc := initializeNativeToolsFixture(t, ctx, conn)
	tools := []any{map[string]any{"type": "function", "name": "theone_native_fixture_read", "description": "Synthetic native callback, never touches user data", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}
	var fresh struct{ Thread struct{ ID string } }
	if err := rpc.call("thread/start", map[string]any{"cwd": dir, "dynamicTools": tools}, &fresh); err != nil {
		t.Fatal(err)
	}
	run := func(id string) string {
		var result any
		if err := rpc.call("turn/start", map[string]any{"threadId": id, "input": []any{map[string]string{"type": "text", "text": "Synthetic tool catalog inspection; answer directly"}}}, &result); err != nil {
			t.Fatal(err)
		}
		waitNativeFixtureTurn(t, rpc)
		select {
		case catalog := <-catalogs:
			return string(catalog)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
			return ""
		}
	}
	if catalog := run(fresh.Thread.ID); !strings.Contains(catalog, "theone_native_fixture_read") {
		t.Fatal("thread/start did not expose native tool")
	}
	var result any
	if err := rpc.call("thread/resume", map[string]any{"threadId": oldID, "cwd": dir, "model": "test-model", "modelProvider": "local_fixture", "dynamicTools": tools}, &result); err != nil {
		t.Fatal(err)
	}
	if catalog := run(oldID); strings.Contains(catalog, "theone_native_fixture_read") {
		t.Log("native CLI supports retrofitting tools into a legacy thread")
	} else {
		t.Log("native CLI ignores dynamicTools on legacy thread/resume")
	}
	conn.Close()
	instance.close()
	instance, err = startSharedCodexProcess(ctx, bin, home, env, nil, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.close()
	conn, err = instance.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rpc = initializeNativeToolsFixture(t, ctx, conn)
	if err := rpc.call("thread/resume", map[string]any{"threadId": fresh.Thread.ID, "cwd": dir}, &result); err != nil {
		t.Fatal(err)
	}
	if catalog := run(fresh.Thread.ID); !strings.Contains(catalog, "theone_native_fixture_read") {
		t.Fatal("cold resume lost persisted native tools")
	}
}
