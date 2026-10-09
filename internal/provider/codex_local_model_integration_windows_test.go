//go:build windows && integration

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/UserExistsError/conpty"
)

// The installed Codex executes a synthetic turn against a loopback Responses
// fixture. Token counts come from the fixture, never a paid/external model.
func TestCodexNativeTokenUsageLocalModelIntegration(t *testing.T) {
	diagnostics := &integrationDiagnostics{}
	var requests atomic.Int32
	var titleRequests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if auth := r.Header.Get("Authorization"); auth != "" && auth != "Bearer TEST_NOT_REAL_NO_MODEL_TURN" {
			t.Error("local fixture received a non-synthetic credential")
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<20))
		var payload struct {
			Model  string
			Stream bool
			Text   struct {
				Format struct {
					Type   string
					Schema struct{ Properties map[string]any }
				}
			}
		}
		if err := json.Unmarshal(body, &payload); err != nil || !payload.Stream {
			t.Error("expected streaming JSON Responses request")
		}
		text := "SYNTHETIC_REPLY"
		suffix := "primary"
		// Native Codex separately names the thread with a structured title
		// request. It has another thread/turn identity and is not a root retry.
		if payload.Text.Format.Type == "json_schema" && payload.Text.Format.Schema.Properties["title"] != nil {
			titleRequests.Add(1)
			text = `{"title":"Synthetic test"}`
			suffix = "title"
		} else {
			requests.Add(1)
			if !strings.Contains(string(body), "SYNTHETIC_LOCAL_TEST") {
				t.Error("root request lacks synthetic prompt")
			}
		}
		r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		event := func(name string, data any) {
			b, _ := json.Marshal(data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
			w.(http.Flusher).Flush()
		}
		response := map[string]any{"id": "resp_local_fixture_" + suffix, "object": "response", "status": "in_progress", "model": payload.Model, "output": []any{}}
		item := map[string]any{"id": "msg_local_fixture_" + suffix, "type": "message", "role": "assistant", "phase": "final_answer", "status": "in_progress", "content": []any{}}
		part := map[string]any{"type": "output_text", "text": text, "annotations": []any{}}
		event("response.created", map[string]any{"type": "response.created", "response": response})
		event("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": 0, "item": item})
		event("response.content_part.added", map[string]any{"type": "response.content_part.added", "item_id": "msg_local_fixture_" + suffix, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
		event("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "item_id": "msg_local_fixture_" + suffix, "output_index": 0, "content_index": 0, "delta": text})
		event("response.output_text.done", map[string]any{"type": "response.output_text.done", "item_id": "msg_local_fixture_" + suffix, "output_index": 0, "content_index": 0, "text": text})
		event("response.content_part.done", map[string]any{"type": "response.content_part.done", "item_id": "msg_local_fixture_" + suffix, "output_index": 0, "content_index": 0, "part": part})
		item["status"] = "completed"
		item["content"] = []any{part}
		event("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
		response["status"] = "completed"
		response["output"] = []any{item}
		response["usage"] = map[string]any{"input_tokens": 100, "input_tokens_details": map[string]int{"cached_tokens": 30}, "output_tokens": 40, "output_tokens_details": map[string]int{"reasoning_tokens": 20}, "total_tokens": 140}
		event("response.completed", map[string]any{"type": "response.completed", "response": response})
	}))
	defer model.Close()
	bin, home, dir, _, env := codexIntegrationFixture(t)
	config := fmt.Sprintf("model = \"test-model\"\nmodel_provider = \"local_fixture\"\n[model_providers.local_fixture]\nname = \"Local fixture\"\nbase_url = %q\nwire_api = \"responses\"\nrequires_openai_auth = false\nsupports_websockets = false\n[features]\nplugins = false\nremote_control = false\n", model.URL+"/v1")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"OPENAI_API_KEY":"TEST_NOT_REAL_NO_MODEL_TURN"}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	changed := make(chan struct{}, 1)
	lookup := func(string) (string, error) { return bin, nil }
	rpc := &CodexRPC{Open: func(ctx context.Context) (io.ReadWriteCloser, error) { return openCodexRPCProcess(ctx, bin, env) }}
	live := NewCodexLive(NewCodex(rpc), codexRelayFactoryWithStderr(lookup, env, diagnostics), func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	spec, err := live.PrepareLaunch(ctx, OpenRequest{Dir: dir, RuntimeToken: "new-codex-local-model"})
	if err != nil {
		t.Fatal(err)
	}
	defer spec.Close()
	tuiEnv := []string{}
	for _, value := range env {
		key, _, _ := strings.Cut(value, "=")
		if !strings.EqualFold(key, "TERM") && !strings.EqualFold(key, "NO_COLOR") {
			tuiEnv = append(tuiEnv, value)
		}
	}
	tuiEnv = append(tuiEnv, spec.Env...)
	tuiEnv = append(tuiEnv, "TERM=xterm-256color")
	p, err := conpty.Start(fmt.Sprintf(`"%s" %s`, bin, strings.Join(spec.Args, " ")), conpty.ConPtyWorkDir(dir), conpty.ConPtyDimensions(100, 30), conpty.ConPtyEnv(tuiEnv))
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	defer once.Do(func() { p.Close() })
	var ready, typed, reply atomic.Bool
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := p.Read(buf)
			if err != nil {
				return
			}
			text := string(buf[:n])
			if strings.Contains(strings.ToLower(text), "test-model") {
				ready.Store(true)
			}
			if strings.Contains(text, "SYNTHETIC_LOCAL_TEST") {
				typed.Store(true)
			}
			if strings.Contains(text, "SYNTHETIC_REPLY") {
				reply.Store(true)
			}
			if strings.Contains(text, "\x1b[6n") {
				p.Write([]byte("\x1b[1;1R"))
			}
			if strings.Contains(text, "\x1b[c") {
				p.Write([]byte("\x1b[?1;2c"))
			}
			select {
			case changed <- struct{}{}:
			default:
			}
		}
	}()
	sent := false
	submitted := false
	for {
		statuses, _ := live.Status(ctx)
		if len(statuses) == 1 {
			if statuses[0].State == "idle" && ready.Load() && !sent {
				sent = true
				if _, err := p.Write([]byte("SYNTHETIC_LOCAL_TEST")); err != nil {
					t.Fatal(err)
				}
			}
			if sent && typed.Load() && !submitted {
				submitted = true
				if _, err := p.Write([]byte("\r")); err != nil {
					t.Fatal(err)
				}
			}
			if statuses[0].TurnStatus == "completed" {
				summary, err := live.Usage(ctx, "new-codex-local-model", dir)
				if err != nil || !summary.SessionFound || summary.SessionTotal.TotalTokens() != 140 || summary.SessionTotal.InputTokens != 70 || summary.SessionTotal.CacheReadInputTokens != 30 || summary.SessionTotal.ThinkingTokens != 20 {
					t.Fatalf("real native usage event: %#v %v", summary, err)
				}
				if requests.Load() != 1 {
					t.Fatal("unexpected root model request count", requests.Load(), diagnostics.String())
				}
				if titleRequests.Load() > 1 {
					t.Fatal("unexpected title request retries", titleRequests.Load())
				}
				if reply.Load() {
					break
				}
			}
			if statuses[0].TurnStatus == "failed" {
				t.Fatal("local fixture turn failed", statuses)
			}
		}
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatalf("local model turn timed out; ready=%v sent=%v typed=%v submitted=%v requests=%d status=%#v", ready.Load(), sent, typed.Load(), submitted, requests.Load(), statuses)
		}
	}
	ids, err := live.RestorableSessions(ctx, true)
	if err != nil || len(ids) != 1 {
		t.Fatalf("completed native thread was not confirmed by official history: %v %v", ids, err)
	}
	live.SealRestoration()
	once.Do(func() { p.Close() })
	spec.Close()
	conn, err := rpc.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	r := newRPCSession(ctx, conn)
	var initialized map[string]any
	if err := r.call("initialize", map[string]any{"clientInfo": map[string]string{"name": "saved_thread_restore_test", "version": "test"}}, &initialized); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("{\"method\":\"initialized\"}\n")); err != nil {
		t.Fatal(err)
	}
	var restored struct{ Thread struct{ ID string } }
	if err := r.call("thread/resume", map[string]string{"threadId": ids[0]}, &restored); err != nil || restored.Thread.ID != ids[0] {
		t.Fatalf("saved native thread could not resume after shutdown: %v %v", restored, err)
	}
}

type integrationDiagnostics struct {
	mu sync.Mutex
	bytes.Buffer
}

func (d *integrationDiagnostics) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.Buffer.Write(p)
}
func (d *integrationDiagnostics) String() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.Buffer.String()
}
