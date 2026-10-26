package provider

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNativeToolRegistrationPreservesProtocolAndOtherTools(t *testing.T) {
	init := []byte(`{"id":"opaque","method":"initialize","params":{"clientInfo":{"name":"codex_cli_rs"},"capabilities":{"futureFlag":true}}}`)
	data, err := registerNativeReferenceTools(init)
	if err != nil || !strings.Contains(string(data), `"experimentalApi":true`) || !strings.Contains(string(data), `"futureFlag":true`) || !strings.Contains(string(data), "codex_cli_rs") {
		t.Fatal(string(data), err)
	}
	data, err = registerNativeReferenceTools([]byte(`{"id":2,"method":"thread/start","params":{"cwd":"fixture","dynamicTools":[{"type":"function","name":"other_tool"}],"future":12}}`))
	if err != nil || !strings.Contains(string(data), "other_tool") || !strings.Contains(string(data), nativeReferenceNamespace) || !strings.Contains(string(data), `"future":12`) {
		t.Fatal(string(data), err)
	}
	if _, err := registerNativeReferenceTools(data); err == nil {
		t.Fatal("namespace collision silently overwritten")
	}
	for _, raw := range []string{`{"id":3,"method":"thread/resume","params":{"threadId":"old"}}`, `{"id":4,"result":{"decision":"accept"}}`} {
		got, err := registerNativeReferenceTools([]byte(raw))
		if err != nil || string(got) != raw {
			t.Fatal(string(got), err)
		}
	}
}

func TestNativeAuxiliaryTitleThreadDoesNotRevokeCurrentReference(t *testing.T) {
	p, source, _ := referenceFixture(t)
	source.nativeTools = true
	ref, err := p.ReferenceSession(context.Background(), source.token, telemetryID)
	if err != nil {
		t.Fatal(err)
	}
	epoch := source.referenceEpoch
	raw := []byte(`{"id":"title","method":"thread/start","params":{"ephemeral":true,"threadSource":"thread_title","dynamicTools":[]}}`)
	source.client(raw)
	if !source.nativeTools || source.referenceEpoch != epoch {
		t.Fatal("auxiliary thread revoked user identity or tool readiness")
	}
	if _, err := source.reference(ref.Reference); err != nil {
		t.Fatal("title generation revoked drag grant", err)
	}
	got, err := registerNativeReferenceTools(raw)
	if err != nil || string(got) != string(raw) {
		t.Fatal("reference tools injected into native title task", string(got), err)
	}
}

func TestNativeThreadRequestCarriesProjectDirectoryWithoutShellQuoting(t *testing.T) {
	const dir = `C:\fixture project\子目录`
	raw := []byte(`{"id":1,"method":"thread/start","params":{"cwd":null}}`)
	got, err := registerNativeReferenceTools(raw, dir)
	var request struct{ Params struct{ Cwd string } }
	json.Unmarshal(got, &request)
	if err != nil || request.Params.Cwd != dir {
		t.Fatal(string(got), err)
	}
}

func TestNativeScopeRejectsAmbiguityAndCannotBorrowRegrantedIdentity(t *testing.T) {
	p, source, _ := referenceFixture(t)
	source.generation = 7
	scope, err := p.nativeReferenceScope(source, source.id)
	if err != nil {
		t.Fatal(err)
	}
	other := p.observe("duplicate", "fixture")
	other.id, other.generation, other.connected = source.id, 7, true
	if _, err := p.nativeReferenceScope(source, source.id); err == nil {
		t.Fatal("ambiguous source accepted")
	}
	other.generation = 8
	if _, err := p.nativeReferenceScope(source, source.id); err != nil {
		t.Fatal("other generation interfered", err)
	}
	source.id = "01900000-0000-7000-8000-000000000088"
	source.referenceEpoch++
	if _, err := p.ReferenceSession(context.Background(), source.token, telemetryID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.referenceRead(context.Background(), source, telemetryID, "", scope); err == nil {
		t.Fatal("old native call borrowed new read grant")
	}
	if _, err := p.referenceSend(context.Background(), source, telemetryID, "not sent", scope); err == nil {
		t.Fatal("old native call borrowed new send grant")
	}
}

func TestNativeIdentityChangeCancelsPendingCallback(t *testing.T) {
	p, source, _ := referenceFixture(t)
	source.generation = 1
	source.turnID = "turn"
	source.turnStatus = "inProgress"
	relay, wire := testRelay(t)
	dialRelay(t, relay)
	source.relay = relay
	host := newNativeReferenceRuntime(p, source, relay)
	defer host.Close()
	started, canceled := make(chan struct{}), make(chan struct{})
	host.execute = func(ctx context.Context, _ *referenceScope, _ string, _ map[string]json.RawMessage) (any, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	}
	message := []byte(`{"id":9,"method":"item/tool/call","params":{"namespace":"` + nativeReferenceNamespace + `","tool":"read_thread","arguments":{},"threadId":"` + source.id + `","turnId":"turn","callId":"pending"}}`)
	if !host.server(message) {
		t.Fatal("own callback leaked")
	}
	<-started
	if _, err := host.client([]byte(`{"id":10,"method":"thread/start","params":{}}`)); err != nil {
		t.Fatal(err)
	}
	<-canceled
	var reply struct {
		ID     int
		Result struct{ Success bool }
	}
	json.Unmarshal([]byte(<-wire.requests), &reply)
	if reply.ID != 9 || reply.Result.Success {
		t.Fatal("identity cancellation did not resolve old native request", reply)
	}
}

func TestNativeExecutionDeadlineStillReplies(t *testing.T) {
	p, source, _ := referenceFixture(t)
	source.generation = 1
	source.turnID = "turn"
	source.turnStatus = "inProgress"
	source.turnStatus = "inProgress"
	relay, wire := testRelay(t)
	dialRelay(t, relay)
	source.relay = relay
	host := newNativeReferenceRuntime(p, source, relay)
	defer host.Close()
	host.execute = func(ctx context.Context, _ *referenceScope, _ string, _ map[string]json.RawMessage) (any, error) {
		host.mu.Lock()
		for _, call := range host.calls {
			call.cancel()
		}
		host.mu.Unlock()
		<-ctx.Done()
		return nil, context.DeadlineExceeded
	}
	if !host.server([]byte(`{"id":91,"method":"item/tool/call","params":{"namespace":"` + nativeReferenceNamespace + `","tool":"read_thread","arguments":{},"threadId":"` + source.id + `","turnId":"turn","callId":"deadline"}}`)) {
		t.Fatal("callback leaked")
	}
	var reply struct {
		ID     int
		Result struct{ Success bool }
	}
	json.Unmarshal([]byte(<-wire.requests), &reply)
	if reply.ID != 91 || reply.Result.Success {
		t.Fatal("execution deadline left native request unresolved", reply)
	}
}

func TestNativeCompletedTurnCannotStartLateTool(t *testing.T) {
	p, source, _ := referenceFixture(t)
	source.generation = 1
	source.turnID = "turn"
	source.turnStatus = "inProgress"
	source.turnStatus = "completed"
	relay, wire := testRelay(t)
	dialRelay(t, relay)
	source.relay = relay
	host := newNativeReferenceRuntime(p, source, relay)
	defer host.Close()
	var calls atomic.Int32
	host.execute = func(context.Context, *referenceScope, string, map[string]json.RawMessage) (any, error) {
		calls.Add(1)
		return true, nil
	}
	host.server([]byte(`{"id":92,"method":"item/tool/call","params":{"namespace":"` + nativeReferenceNamespace + `","tool":"read_thread","arguments":{},"threadId":"` + source.id + `","turnId":"turn","callId":"late"}}`))
	<-wire.requests
	if calls.Load() != 0 {
		t.Fatal("late callback executed after turn completion")
	}
}

func TestNativeDuplicateInFlightRequestGetsOwnResponseWithoutRepeatingWork(t *testing.T) {
	p, source, _ := referenceFixture(t)
	source.generation = 1
	source.turnID = "turn"
	source.turnStatus = "inProgress"
	relay, wire := testRelay(t)
	dialRelay(t, relay)
	source.relay = relay
	host := newNativeReferenceRuntime(p, source, relay)
	defer host.Close()
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	host.execute = func(context.Context, *referenceScope, string, map[string]json.RawMessage) (any, error) {
		calls.Add(1)
		close(started)
		<-release
		return true, nil
	}
	message := `{"id":81,"method":"item/tool/call","params":{"namespace":"` + nativeReferenceNamespace + `","tool":"read_thread","arguments":{},"threadId":"` + source.id + `","turnId":"turn","callId":"in-flight"}}`
	host.server([]byte(message))
	<-started
	host.server([]byte(strings.Replace(message, `"id":81`, `"id":82`, 1)))
	var reply struct {
		ID     int
		Result struct{ Success bool }
	}
	json.Unmarshal([]byte(<-wire.requests), &reply)
	if reply.ID != 82 || reply.Result.Success {
		t.Fatal("duplicate request left pending", reply)
	}
	close(release)
	json.Unmarshal([]byte(<-wire.requests), &reply)
	if reply.ID != 81 || !reply.Result.Success || calls.Load() != 1 {
		t.Fatal(reply, calls.Load())
	}
}

func TestNativeResolvedRequestDoesNotReplyAgainWhenNextTurnStarts(t *testing.T) {
	for _, method := range []string{"serverRequest/resolved", "turn/completed"} {
		t.Run(method, func(t *testing.T) {
			p, source, _ := referenceFixture(t)
			source.generation = 1
			source.turnID = "turn"
			source.turnStatus = "inProgress"
			relay, wire := testRelay(t)
			dialRelay(t, relay)
			source.relay = relay
			host := newNativeReferenceRuntime(p, source, relay)
			defer host.Close()
			started, release := make(chan struct{}), make(chan struct{})
			host.execute = func(context.Context, *referenceScope, string, map[string]json.RawMessage) (any, error) {
				close(started)
				<-release
				return true, nil
			}
			host.server([]byte(`{"id":31,"method":"item/tool/call","params":{"namespace":"` + nativeReferenceNamespace + `","tool":"read_thread","arguments":{},"threadId":"` + source.id + `","turnId":"turn","callId":"resolved"}}`))
			<-started
			host.server([]byte(`{"method":"` + method + `","params":{"threadId":"` + source.id + `","requestId":31,"turn":{"id":"turn"}}}`))
			host.server([]byte(`{"method":"turn/started","params":{"threadId":"` + source.id + `","turn":{"id":"next"}}}`))
			host.replies.Wait()
			select {
			case request := <-wire.requests:
				t.Error("already resolved callback replied again", request)
			default:
			}
			close(release)
		})
	}
}

func TestNativeToolCallbackUsesNativeResponseAndDeduplicatesCalls(t *testing.T) {
	p, source, _ := referenceFixture(t)
	source.generation = 7
	source.turnID = "native-turn"
	source.turnStatus = "inProgress"
	relay, wire := testRelay(t)
	dialRelay(t, relay)
	source.relay = relay
	runtime := newNativeReferenceRuntime(p, source, relay)
	defer runtime.Close()
	var calls atomic.Int32
	runtime.execute = func(context.Context, *referenceScope, string, map[string]json.RawMessage) (any, error) {
		calls.Add(1)
		return map[string]bool{"native": true}, nil
	}
	message := []byte(`{"id":"server-call","method":"item/tool/call","params":{"namespace":"` + nativeReferenceNamespace + `","tool":"list_references","arguments":{},"threadId":"` + source.id + `","turnId":"native-turn","callId":"once"}}`)
	if !runtime.server(message) {
		t.Fatal("native callback not consumed")
	}
	var response struct {
		ID     string
		Result struct {
			Success      bool
			ContentItems []struct{ Type, Text string }
		}
	}
	if err := json.Unmarshal([]byte(<-wire.requests), &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != "server-call" || !response.Result.Success || len(response.Result.ContentItems) != 1 || response.Result.ContentItems[0].Type != "inputText" {
		t.Fatal(response)
	}
	if !runtime.server(message) {
		t.Fatal("duplicate callback leaked")
	}
	<-wire.requests
	if calls.Load() != 1 {
		t.Fatal("duplicate tool executed", calls.Load())
	}
	for _, raw := range []string{`{"id":1,"method":"item/commandExecution/requestApproval","params":{}}`, `{"id":2,"method":"item/tool/call","params":{"namespace":"user_tools","tool":"read_thread"}}`} {
		if runtime.server([]byte(raw)) {
			t.Fatal("foreign request consumed", raw)
		}
	}
}
