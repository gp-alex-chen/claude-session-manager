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
)

// Run one user turn and wait for the native TUI's real title-generation
// turn to finish too. The final sidebar status must still describe the user
// turn; no extra user message is sent to incidentally reset a stuck status.
func TestCodexCompletedUserTurnRemainsIdleAfterNativeTitleIntegration(t *testing.T) {
	var titleRequests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		text := "USER_TURN_FINISHED_FIXTURE"
		if strings.Contains(string(body), `"json_schema"`) {
			titleRequests.Add(1)
			text = `{"title":"Completion fixture"}`
		}
		emitSharedFixtureResponse(w, map[string]any{"type": "message", "id": "msg_completion_fixture", "role": "assistant", "phase": "final_answer", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}})
	}))
	defer model.Close()
	bin, home, dir, _, env := codexIntegrationFixture(t)
	config := fmt.Sprintf("model=\"test-model\"\nmodel_provider=\"local_fixture\"\n[model_providers.local_fixture]\nname=\"Local fixture\"\nbase_url=%q\nwire_api=\"responses\"\nrequires_openai_auth=false\nsupports_websockets=false\n[features]\nplugins=false\nremote_control=false\n[projects.%q]\ntrust_level=\"trusted\"\n", model.URL+"/v1", dir)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"OPENAI_API_KEY":"TEST_NOT_REAL_NO_MODEL_TURN"}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	changed := make(chan struct{}, 1)
	live := newSharedCodexLive(func(string) (string, error) { return bin, nil }, env, home, func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	defer live.CloseService()
	var titleCompleted atomic.Bool
	terminal := startNativePreviewTerminal(t, ctx, live, env, OpenRequest{Dir: dir, RuntimeToken: "completion-fixture"}, changed, func(spec *LaunchSpec) {
		observer := spec.rpc.serverObserver
		spec.rpc.serverObserver = func(data []byte) {
			observer(data)
			var msg observedMessage
			json.Unmarshal(data, &msg)
			if msg.Method != "turn/completed" {
				return
			}
			var params struct {
				ThreadID string
				Turn     struct{ Status string }
			}
			json.Unmarshal(msg.Params, &params)
			o := live.referenceSource("completion-fixture")
			o.mu.Lock()
			foreign := o.id != "" && params.ThreadID != o.id
			o.mu.Unlock()
			if foreign && params.Turn.Status == "completed" {
				titleCompleted.Store(true)
				select {
				case changed <- struct{}{}:
				default:
				}
			}
		}
	})
	defer terminal.Close()
	typed, submitted := false, false
	for {
		status, _ := live.Status(ctx)
		if len(status) == 1 && status[0].State == "idle" && !typed && strings.Contains(terminal.output.String(), "test-model") {
			typed = true
			terminal.pty.Write([]byte("\x1b[200~Complete this synthetic task\x1b[201~"))
		}
		if typed && !submitted && strings.Contains(terminal.output.String(), "Complete this synthetic task") {
			submitted = true
			terminal.pty.Write([]byte("\r"))
		}
		if titleCompleted.Load() && len(status) == 1 && status[0].TurnStatus == "completed" && strings.Contains(terminal.output.String(), "USER_TURN_FINISHED_FIXTURE") {
			if status[0].State != "idle" || status[0].Status != "idle" || titleRequests.Load() != 1 {
				t.Fatalf("completed user task stuck after title: status=%+v titleRequests=%d", status[0], titleRequests.Load())
			}
			return
		}
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatalf("completion fixture timed out: status=%+v titleCompleted=%v titleRequests=%d", status, titleCompleted.Load(), titleRequests.Load())
		}
	}
}
