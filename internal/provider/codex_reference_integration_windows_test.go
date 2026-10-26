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
	"sync/atomic"
	"testing"
	"time"

	"github.com/UserExistsError/conpty"
	"github.com/gp-alex-chen/claude-session-manager/internal/state"
)

// This fixture never uses user credentials, configuration, history or an
// external model. It verifies the native TUI, not just our RPC wrapper.
func TestCodexReferenceNativeDeliveryIntegration(t *testing.T) {
	var rootRequests atomic.Int32
	var draftSubmitted atomic.Bool
	var nativeCatalogSeen atomic.Bool
	var nativeReadSeen atomic.Bool
	var blockResources atomic.Bool
	var resourceCalls atomic.Int32
	unblockResources := make(chan struct{})
	unrelated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     json.RawMessage
			Method string
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		if len(request.ID) == 0 {
			w.WriteHeader(202)
			return
		}
		var result any = map[string]any{}
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}, "resources": map[string]any{}}, "serverInfo": map[string]string{"name": "Unrelated fixture", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{}}
		case "resources/list":
			resourceCalls.Add(1)
			if blockResources.Load() {
				select {
				case <-unblockResources:
				case <-r.Context().Done():
					return
				}
			}
			result = map[string]any{"resources": []any{}}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer unrelated.Close()
	defer close(unblockResources)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		defer r.Body.Close()
		text := "REFERENCE_SYNTHETIC_REPLY"
		if strings.Contains(string(body), `"title"`) && strings.Contains(string(body), `"json_schema"`) {
			text = `{"title":"Reference fixture"}`
		} else {
			n := rootRequests.Add(1)
			var catalog struct {
				Tools []struct {
					Name  string
					Tools []struct{ Name string }
				}
			}
			json.Unmarshal(body, &catalog)
			for _, ns := range catalog.Tools {
				if ns.Name == nativeReferenceNamespace {
					for _, tool := range ns.Tools {
						if tool.Name == "read_thread" {
							nativeCatalogSeen.Store(true)
						}
					}
				}
			}
			if n == 1 {
				args, _ := json.Marshal(map[string]string{"reference": "thread://" + telemetryID})
				emitSharedFixtureResponse(w, map[string]any{"type": "function_call", "id": "fc_native_reference", "call_id": "native_reference_call", "name": "read_thread", "namespace": nativeReferenceNamespace, "arguments": string(args), "status": "completed"})
				return
			}
			if n == 2 {
				nativeReadSeen.Store(strings.Contains(string(body), "SYNTHETIC_HISTORY_ONLY"))

			}
			if n == 1 && strings.Contains(string(body), "PRESERVED_DRAFT") {
				t.Error("unsent draft leaked into remote message")
			}
			if n >= 3 && strings.Contains(string(body), "PRESERVED_DRAFT") {
				draftSubmitted.Store(true)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		event := func(kind string, data any) {
			b, _ := json.Marshal(data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, b)
			w.(http.Flusher).Flush()
		}
		response := map[string]any{"id": "resp_reference_fixture", "object": "response", "status": "in_progress", "model": "test-model", "output": []any{}}
		item := map[string]any{"id": "msg_reference_fixture", "type": "message", "role": "assistant", "phase": "final_answer", "status": "in_progress", "content": []any{}}
		part := map[string]any{"type": "output_text", "text": text, "annotations": []any{}}
		event("response.created", map[string]any{"type": "response.created", "response": response})
		event("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": 0, "item": item})
		event("response.content_part.added", map[string]any{"type": "response.content_part.added", "item_id": "msg_reference_fixture", "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
		event("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "item_id": "msg_reference_fixture", "output_index": 0, "content_index": 0, "delta": text})
		item["status"] = "completed"
		item["content"] = []any{part}
		response["status"] = "completed"
		response["output"] = []any{item}
		response["usage"] = map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}
		event("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
		event("response.completed", map[string]any{"type": "response.completed", "response": response})
	}))
	defer model.Close()
	bin, home, dir, _, env := codexIntegrationFixture(t)
	dir = filepath.Join(dir, "project with spaces")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("model=\"test-model\"\nmodel_provider=\"local_fixture\"\n[model_providers.local_fixture]\nname=\"Local fixture\"\nbase_url=%q\nwire_api=\"responses\"\nrequires_openai_auth=false\nsupports_websockets=false\n[features]\nplugins=false\nremote_control=false\n", model.URL+"/v1")
	// Existing user MCP entries remain under the native client's control.
	config += "\n[mcp_servers.user_disabled_fixture]\ncommand=\"THEONE_FIXTURE_MUST_NOT_EXECUTE\"\nenabled=false\n"
	config += fmt.Sprintf("\n[mcp_servers.unrelated_fixture]\nurl=%q\n", unrelated.URL)
	config += fmt.Sprintf("\n[projects.%q]\ntrust_level=\"trusted\"\n", dir)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"OPENAI_API_KEY":"TEST_NOT_REAL_NO_MODEL_TURN"}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	changed := make(chan struct{}, 1)
	capabilities := state.NewStore(t.TempDir())
	live := newSharedCodexLive(func(string) (string, error) { return bin, nil }, env, home, func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	live.nativeToolState = capabilities
	defer live.CloseService()
	spec, err := live.PrepareLaunch(ctx, OpenRequest{Dir: dir, RuntimeToken: "new-codex-reference-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	defer spec.Close()
	// Run the shell's command lookup too: its inherited PATH may not know
	// Codex, so the launch must provide the resolved executable directory.
	tuiEnv := []string{}
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, "PATH") {
			tuiEnv = append(tuiEnv, entry)
		}
	}
	tuiEnv = append(tuiEnv, spec.Env...)
	tuiEnv = append(tuiEnv, "TERM=xterm-256color")
	p, err := conpty.Start("cmd /d /c codex "+strings.Join(spec.Args, " "), conpty.ConPtyWorkDir(dir), conpty.ConPtyDimensions(120, 35), conpty.ConPtyEnv(tuiEnv))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var ready, draftSeen, messageSeen, replySeen atomic.Bool
	output := &integrationDiagnostics{}
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := p.Read(buf)
			if err != nil {
				return
			}
			s := string(buf[:n])
			output.Write(buf[:n])
			all := output.String()
			ready.Store(strings.Contains(all, "test-model"))
			draftSeen.Store(strings.Contains(all, "PRESERVED_DRAFT"))
			messageSeen.Store(strings.Contains(all, "REFERENCE_REMOTE_INPUT"))
			replySeen.Store(strings.Contains(all, "REFERENCE_SYNTHETIC_REPLY"))
			if strings.Contains(s, "\x1b[6n") {
				p.Write([]byte("\x1b[1;1R"))
			}
			if strings.Contains(s, "\x1b[c") {
				p.Write([]byte("\x1b[?1;2c"))
			}
			select {
			case changed <- struct{}{}:
			default:
			}
		}
	}()
	typed, sent, submitted := false, false, false
	for {
		status, _ := live.Status(ctx)
		if len(status) == 1 && ready.Load() && status[0].State == "idle" && !typed {
			if status[0].Cwd != dir {
				t.Fatalf("native TUI lost its working directory: %q want %q", status[0].Cwd, dir)
			}
			typed = true
			p.Write([]byte("PRESERVED_DRAFT"))
		}
		if len(status) == 1 && draftSeen.Load() && !sent {
			source := live.referenceSource("new-codex-reference-fixture")
			source.mu.Lock()
			ready := source.nativeTools
			source.mu.Unlock()
			if !ready {
				t.Fatal("native thread/start did not register session tools")
			}
			blockResources.Store(true)
			before := resourceCalls.Load()
			ref, err := live.ReferenceSession(ctx, "new-codex-reference-fixture", telemetryID)
			if err != nil || ref.SessionID != telemetryID {
				t.Fatalf("native reference grant failed: %#v %v", ref, err)
			}
			if resourceCalls.Load() != before {
				t.Fatal("drag queried user MCP resources")
			}
			sent = true
			var result map[string]any
			err = spec.rpc.call(ctx, "turn/start", map[string]any{"threadId": status[0].SessionID, "input": []any{map[string]any{"type": "text", "text": "REFERENCE_REMOTE_INPUT"}}}, &result, nil)
			if err != nil {
				t.Fatal(err)
			}
		}
		if sent && !submitted && len(status) == 1 && status[0].TurnStatus == "completed" && messageSeen.Load() && replySeen.Load() {
			submitted = true
			p.Write([]byte("\r"))
		}
		if submitted && draftSubmitted.Load() && len(status) == 1 && status[0].TurnStatus == "completed" {
			rpc := live.History.(*CodexRPC)
			page, err := rpc.ReadConversation(ctx, status[0].SessionID, "")
			if err != nil || len(page.Messages) == 0 {
				t.Fatalf("native paginated read failed: %#v %v", page, err)
			}
			if !nativeCatalogSeen.Load() || !nativeReadSeen.Load() {
				t.Fatalf("native tool catalog/callback failed: catalog=%v read=%v", nativeCatalogSeen.Load(), nativeReadSeen.Load())
			}
			id := status[0].SessionID
			live.SealRestoration()
			ids, err := live.RestorableSessions(ctx, true)
			found := false
			for _, saved := range ids {
				found = found || saved == id
			}
			if err != nil || !found {
				t.Fatalf("final identity not persisted: %v %v", ids, err)
			}
			spec.Close()
			live.CloseService()
			live.BeginService()
			_, restored, err := live.shared.connect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			rr := initializeSharedFixture(t, ctx, restored)
			var resumed struct{ Thread struct{ ID string } }
			if err := rr.call("thread/resume", map[string]any{"threadId": id, "cwd": dir}, &resumed); err != nil || resumed.Thread.ID != id {
				t.Fatalf("saved session not restored after shutdown/startup: %s %v", resumed.Thread.ID, err)
			}
			return
		}
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatalf("native reference delivery not visible; input=%v reply=%v status=%#v output=%s", messageSeen.Load(), replySeen.Load(), status, output.String())
		}
	}
}
