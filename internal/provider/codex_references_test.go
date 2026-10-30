package provider

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

type referenceHistory struct{}
type pausedReferenceRead struct{ started, release chan struct{} }

func (h *pausedReferenceRead) ListSessions(ctx context.Context) ([]Session, error) {
	return referenceHistory{}.ListSessions(ctx)
}
func (h *pausedReferenceRead) ReadConversation(context.Context, string, string) (ReferencePage, error) {
	close(h.started)
	<-h.release
	return ReferencePage{Messages: []ReferenceText{{Role: "assistant", Text: "old epoch"}}}, nil
}

type blockedReferenceHistory struct{ started, release chan struct{} }

func (h *blockedReferenceHistory) ListSessions(context.Context) ([]Session, error) {
	close(h.started)
	<-h.release
	return referenceHistory{}.ListSessions(context.Background())
}

func (referenceHistory) ListSessions(context.Context) ([]Session, error) {
	return []Session{{Ref: SessionRef{Codex, telemetryID}, Name: "Target fixture"}}, nil
}
func (referenceHistory) ReadConversation(context.Context, string, string) (ReferencePage, error) {
	return ReferencePage{Messages: []ReferenceText{{Role: "assistant", Text: "Fixture answer"}}}, nil
}

func referenceFixture(t *testing.T) (*CodexLive, *codexObservation, *codexObservation) {
	p := NewCodexLive(NewCodex(referenceHistory{}), nil, nil)
	source := p.observe("source", `C:\fixture`)
	source.id = "01900000-0000-7000-8000-000000000099"
	source.active = true
	source.connected = true
	source.state = "idle"
	target := p.observe("target", `C:\fixture`)
	target.id = telemetryID
	target.active = true
	target.connected = true
	target.state = "idle"
	r, _ := testRelay(t)
	dialRelay(t, r)
	source.relay = r
	target.relay = r
	return p, source, target
}

func TestReferencesAreExplicitScopedAndRevokedOnIdentitySwitch(t *testing.T) {
	p, source, _ := referenceFixture(t)
	if _, err := p.referenceRead(context.Background(), source, "not-granted", ""); err == nil {
		t.Fatal("ungranted read accepted")
	}
	ref, err := p.ReferenceSession(context.Background(), "source", telemetryID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := p.referenceRead(context.Background(), source, ref.Reference, "")
	if err != nil || page.Messages[0].Text != "Fixture answer" {
		t.Fatal(page, err)
	}
	if _, err := p.referenceRead(context.Background(), p.observe("other", `C:\fixture`), ref.Reference, ""); err == nil {
		t.Fatal("cross source grant accepted")
	}
	source.client([]byte(`{"id":11,"method":"thread/start","params":{}}`))
	if _, err := p.referenceRead(context.Background(), source, ref.Reference, ""); err == nil {
		t.Fatal("identity transition retained access")
	}
	source.server([]byte(`{"id":11,"result":{"thread":{"id":"01900000-0000-7000-8000-000000000088","status":{"type":"idle"}}}}`))
	if _, err := p.referenceRead(context.Background(), source, ref.Reference, ""); err == nil {
		t.Fatal("new identity retained grant")
	}
}

func TestCompactThreadMentionsResolveOnlyExplicitGrants(t *testing.T) {
	p, source, _ := referenceFixture(t)
	for _, value := range []string{telemetryID, "thread://" + telemetryID} {
		if _, err := p.referenceRead(context.Background(), source, value, ""); err == nil {
			t.Fatal("ungranted UUID accepted:", value)
		}
	}
	if _, err := p.ReferenceSession(context.Background(), source.token, telemetryID); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{telemetryID, "thread://" + telemetryID, "thread://" + telemetryID + "?hostId=local"} {
		if _, err := p.referenceRead(context.Background(), source, value, ""); err != nil {
			t.Fatal("granted compact mention failed:", value, err)
		}
	}
	for _, value := range []string{"01900000-0000-7000-8000-000000000055", "thread://" + telemetryID + "?hostId=remote", "thread://" + telemetryID + "/unexpected"} {
		if _, err := p.referenceRead(context.Background(), source, value, ""); err == nil {
			t.Fatal("ungranted/remote reference accepted:", value)
		}
	}
}

func TestCompactMentionReadCannotCrossARegrantedIdentityEpoch(t *testing.T) {
	p, source, _ := referenceFixture(t)
	h := &pausedReferenceRead{started: make(chan struct{}), release: make(chan struct{})}
	p.History = h
	if _, err := p.ReferenceSession(context.Background(), source.token, telemetryID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := p.referenceRead(context.Background(), source, telemetryID, ""); done <- err }()
	<-h.started
	id := source.id
	source.client([]byte(`{"id":61,"method":"thread/resume","params":{"threadId":"` + id + `"}}`))
	source.server([]byte(`{"id":61,"result":{"thread":{"id":"` + id + `","status":{"type":"idle"}}}}`))
	if _, err := p.ReferenceSession(context.Background(), source.token, telemetryID); err != nil {
		t.Fatal(err)
	}
	close(h.release)
	if err := <-done; err == nil {
		t.Fatal("old read crossed new drag authorization")
	}
}

func TestReferenceGrantRejectsLegacySourceWithoutNativeTools(t *testing.T) {
	p, source, _ := referenceFixture(t)
	p.nativeTools = true
	if _, err := p.ReferenceSession(context.Background(), source.token, telemetryID); err == nil || !strings.Contains(err.Error(), "新建") {
		t.Fatal(err)
	}
	source.nativeTools = true
	if _, err := p.ReferenceSession(context.Background(), source.token, telemetryID); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceGrantCannotReviveAfterResumeSameIdentity(t *testing.T) {
	p, source, _ := referenceFixture(t)
	ref, err := p.ReferenceSession(context.Background(), source.token, telemetryID)
	if err != nil {
		t.Fatal(err)
	}
	id := source.id
	source.client([]byte(`{"id":22,"method":"thread/resume","params":{"threadId":"` + id + `"}}`))
	source.server([]byte(`{"id":22,"result":{"thread":{"id":"` + id + `","status":{"type":"idle"}}}}`))
	if _, err := p.referenceRead(context.Background(), source, ref.Reference, ""); err == nil {
		t.Fatal("old grant revived after same-ID resume")
	}
}

func TestReferenceGrantCannotCrossAnAsyncIdentityEpoch(t *testing.T) {
	p, source, _ := referenceFixture(t)
	h := &blockedReferenceHistory{started: make(chan struct{}), release: make(chan struct{})}
	p.History = h
	done := make(chan error, 1)
	go func() { _, err := p.ReferenceSession(context.Background(), source.token, telemetryID); done <- err }()
	<-h.started
	original := source.id
	source.client([]byte(`{"id":31,"method":"thread/start","params":{}}`))
	source.server([]byte(`{"id":31,"result":{"thread":{"id":"01900000-0000-7000-8000-000000000077","status":{"type":"idle"}}}}`))
	source.client([]byte(`{"id":32,"method":"thread/resume","params":{"threadId":"` + original + `"}}`))
	source.server([]byte(`{"id":32,"result":{"thread":{"id":"` + original + `","status":{"type":"idle"}}}}`))
	close(h.release)
	if err := <-done; err == nil {
		t.Fatal("asynchronous grant crossed A->B->A")
	}
}

func TestReferenceConcurrentSendsReserveOnlyOneTargetTurn(t *testing.T) {
	p, source, target := referenceFixture(t)
	ref, err := p.ReferenceSession(context.Background(), source.token, target.id)
	if err != nil {
		t.Fatal(err)
	}
	relay, f := testRelay(t)
	dialRelay(t, relay)
	target.relay = relay
	done := make(chan error, 1)
	go func() { _, err := p.referenceSend(context.Background(), source, ref.Reference, "first"); done <- err }()
	var request map[string]json.RawMessage
	json.Unmarshal([]byte(<-f.requests), &request)
	if _, err := p.referenceSend(context.Background(), source, ref.Reference, "second"); err == nil {
		t.Fatal("second send steered active first turn")
	}
	go f.output.Write([]byte(`{"id":` + string(request["id"]) + `,"result":{"turn":{"id":"first-turn"}}}` + "\n"))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-f.requests:
		t.Fatal("extra turn written", request)
	default:
	}
}

func TestNativeFailedTurnReservationIgnoresUsageOnlyUpdates(t *testing.T) {
	_, _, target := referenceFixture(t)
	target.client([]byte(`{"id":41,"method":"turn/start","params":{"threadId":"` + target.id + `"}}`))
	target.server([]byte(`{"method":"thread/tokenUsage/updated","params":{"threadId":"` + target.id + `","tokenUsage":{"total":{"totalTokens":12,"inputTokens":10,"outputTokens":2},"last":{"totalTokens":12,"inputTokens":10,"outputTokens":2}}}}`))
	target.server([]byte(`{"id":41,"error":{"message":"synthetic failure"}}`))
	target.mu.Lock()
	defer target.mu.Unlock()
	if target.state != "idle" {
		t.Fatal("usage prevented reservation recovery:", target.state)
	}
}

func TestReferenceSendDoesNotOverwriteAnAlreadyCompletedTurn(t *testing.T) {
	p, source, target := referenceFixture(t)
	ref, err := p.ReferenceSession(context.Background(), source.token, target.id)
	if err != nil {
		t.Fatal(err)
	}
	relay, f := testRelay(t)
	ws := dialRelay(t, relay)
	target.relay = relay
	relay.serverObserver = target.server
	go func() {
		var request map[string]json.RawMessage
		json.Unmarshal([]byte(<-f.requests), &request)
		f.output.Write([]byte(`{"method":"turn/completed","params":{"threadId":"` + target.id + `","turn":{"id":"fast-turn","status":"completed"}}}` + "\n" + `{"id":` + string(request["id"]) + `,"result":{"turn":{"id":"fast-turn"}}}` + "\n"))
	}()
	go ws.ReadMessage()
	if _, err := p.referenceSend(context.Background(), source, ref.Reference, "fixture message"); err != nil {
		t.Fatal(err)
	}
	target.mu.Lock()
	defer target.mu.Unlock()
	if target.state != "idle" {
		t.Fatal("completed target left busy:", target.state)
	}
}

func TestReferenceSendRejectsBusyPendingDuplicateAndDisconnectedTargets(t *testing.T) {
	p, source, target := referenceFixture(t)
	ref, err := p.ReferenceSession(context.Background(), source.token, target.id)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"working", "blocked", "unknown"} {
		target.state = state
		if _, err := p.referenceSend(context.Background(), source, ref.Reference, "hello"); err == nil {
			t.Fatalf("accepted %s", state)
		}
	}
	target.state = "idle"
	target.client([]byte(`{"id":91,"method":"turn/start","params":{"threadId":"` + target.id + `"}}`))
	if _, err := p.referenceSend(context.Background(), source, ref.Reference, "hello"); err == nil {
		t.Fatal("native pending turn accepted")
	}
	target.server([]byte(`{"id":91,"error":{"message":"fixture"}}`))
	duplicate := p.observe("duplicate", `C:\fixture`)
	duplicate.id = target.id
	duplicate.connected = true
	if _, err := p.referenceSend(context.Background(), source, ref.Reference, "hello"); err == nil {
		t.Fatal("ambiguous runtime accepted")
	}
	duplicate.active = false
	target.disconnected()
	if _, err := p.referenceSend(context.Background(), source, ref.Reference, "hello"); err == nil {
		t.Fatal("disconnected target accepted")
	}
}

func TestConversationReadUsesPagedOfficialHistoryAndFiltersToolOutput(t *testing.T) {
	body := `{"id":1,"result":{}}
{"id":2,"result":{"data":[{"id":"turn-fixture","items":[{"type":"userMessage","content":[{"type":"text","text":"Question"}]},{"type":"commandExecution","aggregatedOutput":"PRIVATE_TOOL_OUTPUT"},{"type":"agentMessage","text":"Answer"}]}],"nextCursor":"next-page"}}
`
	f := &rpcFixture{Reader: strings.NewReader(body)}
	rpc := &CodexRPC{Open: func(context.Context) (io.ReadWriteCloser, error) { return f, nil }}
	page, err := rpc.ReadConversation(context.Background(), telemetryID, "cursor")
	if err != nil || len(page.Messages) != 2 || page.Messages[1].Text != "Answer" || page.NextCursor == nil || *page.NextCursor != "next-page" {
		t.Fatal(page, err)
	}
	b, _ := json.Marshal(page)
	if strings.Contains(string(b), "PRIVATE_TOOL_OUTPUT") {
		t.Fatal("tool output exposed")
	}
	if f.requests[2]["method"] != "thread/turns/list" {
		t.Fatal(f.requests)
	}
	params := f.requests[2]["params"].(map[string]any)
	if params["cursor"] != "cursor" || params["itemsView"] != "full" {
		t.Fatal(params)
	}
}
