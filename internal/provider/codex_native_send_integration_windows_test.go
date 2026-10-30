//go:build windows && integration

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/UserExistsError/conpty"
)

type nativePreviewTerminal struct {
	spec   LaunchSpec
	pty    *conpty.ConPty
	output *integrationDiagnostics
}

func (t *nativePreviewTerminal) Close() { t.pty.Close(); t.spec.Close() }
func startNativePreviewTerminal(t *testing.T, ctx context.Context, live *CodexLive, env []string, req OpenRequest, changed chan<- struct{}, configure ...func(*LaunchSpec)) *nativePreviewTerminal {
	t.Helper()
	spec, err := live.PrepareLaunch(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, configureSpec := range configure {
		configureSpec(&spec)
	}
	childEnv := []string{}
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "PATH") && !strings.EqualFold(key, "TERM") {
			childEnv = append(childEnv, entry)
		}
	}
	childEnv = append(childEnv, spec.Env...)
	childEnv = append(childEnv, "TERM=xterm-256color")
	pty, err := conpty.Start("cmd /d /c codex "+strings.Join(spec.Args, " "), conpty.ConPtyWorkDir(req.Dir), conpty.ConPtyDimensions(120, 35), conpty.ConPtyEnv(childEnv))
	if err != nil {
		spec.Close()
		t.Fatal(err)
	}
	terminal := &nativePreviewTerminal{spec, pty, &integrationDiagnostics{}}
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := pty.Read(buf)
			if err != nil {
				return
			}
			terminal.output.Write(buf[:n])
			text := string(buf[:n])
			if strings.Contains(text, "\x1b[6n") {
				pty.Write([]byte("\x1b[1;1R"))
			}
			if strings.Contains(text, "\x1b[c") {
				pty.Write([]byte("\x1b[?1;2c"))
			}
			select {
			case changed <- struct{}{}:
			default:
			}
		}
	}()
	return terminal
}

func TestNativeToolSendsToLegacyTerminalAndPreservesDraftIntegration(t *testing.T) {
	var sourceCalls, targetCalls atomic.Int32
	var accepted, draftSubmitted atomic.Bool
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Text  struct{ Format struct{ Type string } }
			Input []struct {
				Type, Role string
				Content    []struct{ Type, Text string }
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if request.Text.Format.Type == "json_schema" {
			emitSharedFixtureResponse(w, map[string]any{"type": "message", "id": "title_native_fixture", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "{\"title\":\"Native send fixture\"}", "annotations": []any{}}}})
			return
		}
		lastUser := ""
		for _, item := range request.Input {
			if item.Role == "user" {
				lastUser = ""
				for _, part := range item.Content {
					lastUser += part.Text
				}
			}
		}
		text := "NATIVE_FIXTURE_REPLY"
		if strings.Contains(lastUser, "NATIVE_SEND_REQUEST") {
			if sourceCalls.Add(1) == 1 {
				args, _ := json.Marshal(map[string]string{"reference": "thread://" + telemetryID, "message": "NATIVE_DELIVER_TO_B"})
				emitSharedFixtureResponse(w, map[string]any{"type": "function_call", "id": "fc_native_send", "call_id": "send_once", "namespace": nativeReferenceNamespace, "name": "send_message_to_thread", "arguments": string(args), "status": "completed"})
				return
			}
			accepted.Store(true)
			text = "A_SEND_ACCEPTED"
		} else if strings.Contains(lastUser, "TARGET_DRAFT_KEEP") {
			draftSubmitted.Store(true)
			text = "B_DRAFT_REPLY"
		} else if strings.Contains(lastUser, "NATIVE_DELIVER_TO_B") {
			targetCalls.Add(1)
			text = "B_NATIVE_REPLY"
		}
		emitSharedFixtureResponse(w, map[string]any{"type": "message", "id": "msg_native_send", "role": "assistant", "phase": "final_answer", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}})
	}))
	defer model.Close()
	bin, home, dir, _, env := codexIntegrationFixture(t)
	// The legacy fixture must also point at the local fake provider. Real
	// resumed sessions correctly retain their saved provider selection.
	rollouts, err := filepath.Glob(filepath.Join(home, "sessions", "*", "*", "*", "*.jsonl"))
	if err != nil || len(rollouts) != 1 {
		t.Fatal(rollouts, err)
	}
	data, err := os.ReadFile(rollouts[0])
	if err != nil {
		t.Fatal(err)
	}
	content := strings.Replace(string(data), `"model_provider":"openai"`, `"model_provider":"local_fixture"`, 1)
	if err := os.WriteFile(rollouts[0], []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("model=\"test-model\"\nmodel_provider=\"local_fixture\"\n[model_providers.local_fixture]\nname=\"Local fixture\"\nbase_url=%q\nwire_api=\"responses\"\nrequires_openai_auth=false\nsupports_websockets=false\n[features]\nplugins=false\nremote_control=false\n[projects.%q]\ntrust_level=\"trusted\"\n", model.URL+"/v1", dir)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"OPENAI_API_KEY":"TEST_NOT_REAL_NO_MODEL_TURN"}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	changed := make(chan struct{}, 1)
	live := newSharedCodexLive(func(string) (string, error) { return bin, nil }, env, home, func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	defer live.CloseService()
	a := startNativePreviewTerminal(t, ctx, live, env, OpenRequest{Dir: dir, RuntimeToken: "native-source"}, changed)
	defer a.Close()
	b := startNativePreviewTerminal(t, ctx, live, env, OpenRequest{Dir: dir, RuntimeToken: "legacy-target", Ref: &SessionRef{Codex, telemetryID}}, changed)
	defer b.Close()
	typed, sent, sourceSubmitted, submitted := false, false, false, false
	for {
		list, _ := live.Status(ctx)
		sourceIdle, targetIdle := false, false
		for _, status := range list {
			if status.RuntimeToken == "native-source" {
				sourceIdle = status.State == "idle"
			}
			if status.RuntimeToken == "legacy-target" {
				targetIdle = status.State == "idle"
			}
		}
		if sourceIdle && targetIdle && !typed && strings.Contains(b.output.String(), "test-model") {
			typed = true
			b.pty.Write([]byte("\x1b[200~TARGET_DRAFT_KEEP\x1b[201~"))
		}
		if typed && !sent && strings.Contains(b.output.String(), "TARGET_DRAFT_KEEP") {
			if _, err := live.ReferenceSession(ctx, "native-source", telemetryID); err != nil {
				t.Fatal(err)
			}
			sent = true
			a.pty.Write([]byte("\x1b[200~NATIVE_SEND_REQUEST\x1b[201~"))
		}
		if sent && !sourceSubmitted && strings.Contains(a.output.String(), "NATIVE_SEND_REQUEST") {
			sourceSubmitted = true
			a.pty.Write([]byte("\r"))
		}
		if sent && accepted.Load() && targetIdle && !submitted && strings.Contains(b.output.String(), "B_NATIVE_REPLY") {
			submitted = true
			b.pty.Write([]byte("\r"))
		}
		if submitted && draftSubmitted.Load() && targetIdle && strings.Contains(b.output.String(), "B_DRAFT_REPLY") {
			if targetCalls.Load() != 1 {
				t.Fatal("native send repeated", targetCalls.Load())
			}
			return
		}
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatalf("native send did not finish: sourceCalls=%d targetCalls=%d accepted=%v draft=%v A=%s B=%s", sourceCalls.Load(), targetCalls.Load(), accepted.Load(), draftSubmitted.Load(), a.output.String(), b.output.String())
		}
	}
}
