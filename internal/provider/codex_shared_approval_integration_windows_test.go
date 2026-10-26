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

func emitSharedFixtureResponse(w http.ResponseWriter, item map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(kind string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, b)
		w.(http.Flusher).Flush()
	}
	response := map[string]any{"id": "resp_shared_approval", "object": "response", "model": "test-model", "status": "in_progress", "output": []any{}}
	emit("response.created", map[string]any{"type": "response.created", "response": response})
	emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
	response["status"] = "completed"
	response["output"] = []any{item}
	response["usage"] = map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}
	emit("response.completed", map[string]any{"type": "response.completed", "response": response})
}

func TestCodexSharedApprovalIsolationIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	blocked, release := make(chan struct{}, 1), make(chan struct{})
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		if strings.Contains(string(body), "B_CONTINUES") {
			select {
			case blocked <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-ctx.Done():
				return
			}
			emitSharedFixtureResponse(w, map[string]any{"type": "message", "id": "msg_shared_approval", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "B_FINISHED", "annotations": []any{}}}})
			return
		}
		emitSharedFixtureResponse(w, map[string]any{"type": "function_call", "id": "fc_shared_approval", "call_id": "shared_approval_call", "name": "exec_command", "arguments": `{"cmd":"echo THEONE_SYNTHETIC_APPROVAL","sandbox_permissions":"require_escalated","justification":"Synthetic shared connection isolation test"}`, "status": "completed"})
	}))
	defer model.Close()
	// Unblock handlers before httptest.Close even on a failed assertion.
	defer close(release)
	bin, home, dir, _, env := codexIntegrationFixture(t)
	config := fmt.Sprintf("model=\"test-model\"\nmodel_provider=\"local_fixture\"\n[model_providers.local_fixture]\nname=\"Local fixture\"\nbase_url=%q\nwire_api=\"responses\"\nrequires_openai_auth=false\nsupports_websockets=false\n[features]\nplugins=false\nremote_control=false\n", model.URL+"/v1")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	owner := newSharedCodexOwner(func(ctx context.Context, g uint64) (*sharedCodexInstance, error) {
		return startSharedCodexProcess(ctx, bin, home, env, nil, g, nil)
	})
	defer owner.Close()
	_, a, err := owner.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	_, b, err := owner.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	_, metadata, err := owner.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer metadata.Close()
	ra, rb, rm := initializeSharedFixture(t, ctx, a), initializeSharedFixture(t, ctx, b), initializeSharedFixture(t, ctx, metadata)
	var ta, tb struct{ Thread struct{ ID string } }
	params := map[string]any{"cwd": dir, "approvalPolicy": "on-request", "sandbox": "read-only"}
	if err := ra.call("thread/start", params, &ta); err != nil {
		t.Fatal(err)
	}
	if err := rb.call("thread/start", params, &tb); err != nil {
		t.Fatal(err)
	}
	var result any
	if err := rb.call("turn/start", map[string]any{"threadId": tb.Thread.ID, "input": []any{map[string]string{"type": "text", "text": "B_CONTINUES"}}}, &result); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocked:
	case <-ctx.Done():
		t.Fatal("B did not start", ctx.Err())
	}
	if err := ra.call("turn/start", map[string]any{"threadId": ta.Thread.ID, "input": []any{map[string]string{"type": "text", "text": "A_REQUIRES_APPROVAL"}}}, &result); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case read := <-ra.reads:
			if read.err != nil {
				t.Fatal(read.err)
			}
			if read.msg.ID != nil && strings.Contains(read.msg.Method, "requestApproval") {
				goto approved
			}
		case <-ctx.Done():
			t.Fatal("A approval not received", ctx.Err())
		}
	}
approved:
	// A metadata operation would fail on any misrouted server request.
	if err := rm.call("thread/loaded/list", map[string]any{}, &result); err != nil {
		t.Fatal("approval leaked to metadata", err)
	}
	a.Close()
	// B must remain usable while its model request is pending. A misrouted
	// approval would fail this call before receiving its ordinary response.
	if err := rb.call("thread/loaded/list", map[string]any{}, &result); err != nil {
		t.Fatal("approval leaked to B or A close stopped B", err)
	}
	// A single release value wakes the one pending B response.
	release <- struct{}{}
	for {
		select {
		case read := <-rb.reads:
			if read.err != nil {
				t.Fatal(read.err)
			}
			if read.msg.ID != nil && read.msg.Method != "" {
				t.Fatal("approval leaked to B", read.msg.Method)
			}
			if read.msg.Method == "turn/completed" {
				return
			}
		case <-ctx.Done():
			t.Fatal("B did not finish after A closed", ctx.Err())
		}
	}
}
