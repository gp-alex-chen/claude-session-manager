package provider

import (
	"context"
	"testing"
	"time"
)

type mutableLiveHistory struct {
	sessions []Session
	calls    int
}

func (h *mutableLiveHistory) ListSessions(context.Context) ([]Session, error) {
	h.calls++
	return h.sessions, nil
}

func TestCodexNativeDurabilityRequiresHistoryAndRefreshesAfterFirstTurn(t *testing.T) {
	h := &mutableLiveHistory{}
	live := NewCodexLive(NewCodex(h), nil, nil)
	o := live.observe("new-codex-save", `C:\work`)
	o.client([]byte(`{"id":1,"method":"thread/start","params":{}}`))
	o.server([]byte(`{"id":1,"result":{"thread":{"id":"` + telemetryID + `","status":{"type":"idle"}}}}`))
	ids, err := live.RestorableSessions(context.Background(), false)
	if err != nil || len(ids) != 0 {
		t.Fatal("empty thread claimed restorable", ids, err)
	}
	// An already cached negative result must not delay the first saved turn.
	h.sessions = []Session{{Ref: SessionRef{Codex, telemetryID}, Dir: `C:\work`, Time: time.Unix(10, 0)}}
	o.server([]byte(`{"method":"turn/started","params":{"threadId":"` + telemetryID + `","turn":{"id":"first"}}}`))
	ids, err = live.RestorableSessions(context.Background(), false)
	if err != nil || len(ids) != 1 || ids[0] != telemetryID || h.calls != 2 {
		t.Fatal("saved first turn not confirmed", ids, err, h.calls)
	}
	// A confirmed ID needs no extra history process on every token event.
	_, _ = live.RestorableSessions(context.Background(), false)
	if h.calls != 2 {
		t.Fatal("confirmed identity queried again", h.calls)
	}
}

func TestCodexIdentityRepliesCannotRevertNewerThreadAndSealIsFinal(t *testing.T) {
	live := NewCodexLive(NewCodex(nil), nil, nil)
	o := live.observe("new-codex-order", `C:\work`)
	for _, request := range []string{"1", "2"} {
		o.client([]byte(`{"id":` + request + `,"method":"thread/start","params":{}}`))
	}
	newID := "01900000-0000-7000-8000-000000000002"
	o.server([]byte(`{"id":2,"result":{"thread":{"id":"` + newID + `","status":{"type":"idle"}}}}`))
	o.server([]byte(`{"id":1,"result":{"thread":{"id":"` + telemetryID + `","status":{"type":"idle"}}}}`))
	list, _ := live.Status(context.Background())
	if len(list) != 1 || list[0].SessionID != newID {
		t.Fatal("late response reverted thread", list)
	}
	live.SealRestoration()
	o.client([]byte(`{"id":3,"method":"thread/resume","params":{"threadId":"` + telemetryID + `"}}`))
	o.server([]byte(`{"id":3,"result":{"thread":{"id":"` + telemetryID + `","status":{"type":"idle"}}}}`))
	list, _ = live.Status(context.Background())
	if list[0].SessionID != newID {
		t.Fatal("identity changed after shutdown snapshot was sealed", list)
	}
}

func TestCodexEphemeralTitleThreadCannotReplaceMainIdentity(t *testing.T) {
	live := NewCodexLive(NewCodex(nil), nil, nil)
	o := seedTelemetry(t, live, "new-codex-main")
	o.client([]byte(`{"id":2,"method":"thread/start","params":{"ephemeral":true}}`))
	o.server([]byte(`{"id":2,"result":{"thread":{"id":"01900000-0000-7000-8000-000000000002","ephemeral":true,"status":{"type":"idle"}}}}`))
	list, _ := live.Status(context.Background())
	if len(list) != 1 || list[0].SessionID != telemetryID {
		t.Fatal("ephemeral title replaced main thread", list)
	}
}

func TestCodexAuxiliaryTurnCannotKeepCompletedMainWorking(t *testing.T) {
	for _, outcome := range []string{"completed", "failed", "interrupted"} {
		t.Run(outcome, func(t *testing.T) {
			live := NewCodexLive(NewCodex(nil), nil, nil)
			o := seedTelemetry(t, live, "main-with-title")
			o.server([]byte(`{"method":"turn/started","params":{"threadId":"` + telemetryID + `","turn":{"id":"main-turn"}}}`))
			o.server([]byte(`{"method":"turn/completed","params":{"threadId":"` + telemetryID + `","turn":{"id":"main-turn","status":"` + outcome + `"}}}`))
			before, _ := live.Status(context.Background())
			const titleID = "01900000-0000-7000-8000-000000000002"
			o.client([]byte(`{"id":"title-start","method":"thread/start","params":{"ephemeral":true,"threadSource":"thread_title"}}`))
			o.server([]byte(`{"id":"title-start","result":{"thread":{"id":"` + titleID + `","ephemeral":true,"status":{"type":"idle"}}}}`))
			o.client([]byte(`{"id":"title-turn","method":"turn/start","params":{"threadId":"` + titleID + `"}}`))
			// The title is still running: it must never mark the user's
			// completed/failed/interrupted task as working, even temporarily.
			after, _ := live.Status(context.Background())
			if after[0].State != before[0].State || after[0].Status != before[0].Status || after[0].TurnStatus != outcome || len(o.nativeTurns) != 0 {
				t.Fatalf("auxiliary turn changed main status: before=%+v after=%+v pending=%d", before[0], after[0], len(o.nativeTurns))
			}
			o.server([]byte(`{"id":"title-turn","result":{"turn":{"id":"title-turn"}}}`))
			o.server([]byte(`{"method":"turn/completed","params":{"threadId":"` + titleID + `","turn":{"id":"title-turn","status":"completed"}}}`))
			after, _ = live.Status(context.Background())
			if after[0].State != before[0].State || after[0].TurnID != "main-turn" || after[0].TurnStatus != outcome {
				t.Fatal("auxiliary completion changed main task", after)
			}
		})
	}
}

func TestCodexOnlyBoundUserTurnReservesBusyState(t *testing.T) {
	for _, tc := range []struct {
		name, params string
		own          bool
	}{
		{"bound", `{"threadId":"` + telemetryID + `"}`, true},
		{"foreign", `{"threadId":"01900000-0000-7000-8000-000000000002"}`, false},
		{"missing", `{}`, false},
		{"malformed", `{"threadId":23}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live := NewCodexLive(NewCodex(nil), nil, nil)
			o := seedTelemetry(t, live, "bound-turn")
			o.client([]byte(`{"id":"request","method":"turn/start","params":` + tc.params + `}`))
			status, _ := live.Status(context.Background())
			if tc.own {
				if status[0].State != "working" || len(o.nativeTurns) != 1 {
					t.Fatal("own turn did not reserve busy state", status)
				}
			} else if status[0].State != "idle" || len(o.nativeTurns) != 0 {
				t.Fatal("unrelated request reserved user state", status)
			}
			o.server([]byte(`{"id":"request","error":{"message":"fixture failure"}}`))
			status, _ = live.Status(context.Background())
			if status[0].State != "idle" || len(o.nativeTurns) != 0 {
				t.Fatal("rejected request left the terminal busy", status)
			}
		})
	}
}

const telemetryID = "01900000-0000-7000-8000-000000000001"

func seedTelemetry(t *testing.T, live *CodexLive, token string) *codexObservation {
	t.Helper()
	o := live.observe(token, `C:\work`)
	o.client([]byte(`{"id":"resume-request","method":"thread/resume","params":{"threadId":"` + telemetryID + `"}}`))
	o.server([]byte(`{"id":"resume-request","result":{"thread":{"id":"` + telemetryID + `","cwd":"C:\\work","status":{"type":"idle"}}}}`))
	return o
}

func TestCodexTelemetrySeparatesWaitingCompletionFailureAndDisconnect(t *testing.T) {
	live := NewCodexLive(NewCodex(nil), nil, nil)
	o := seedTelemetry(t, live, "new-codex-test")
	send := func(method, params string) { o.server([]byte(`{"method":"` + method + `","params":` + params + `}`)) }
	send("thread/status/changed", `{"threadId":"`+telemetryID+`","status":{"type":"active","activeFlags":["waitingOnApproval"]}}`)
	list, _ := live.Status(context.Background())
	if len(list) != 1 || list[0].State != "blocked" || list[0].WaitingFor != "approval" || list[0].RuntimeToken != "new-codex-test" {
		t.Fatalf("approval: %#v", list)
	}
	send("thread/status/changed", `{"threadId":"`+telemetryID+`","status":{"type":"active","activeFlags":["waitingOnUserInput"]}}`)
	list, _ = live.Status(context.Background())
	if list[0].WaitingFor != "userInput" {
		t.Fatal(list)
	}
	for _, outcome := range []string{"completed", "failed", "interrupted"} {
		send("turn/started", `{"threadId":"`+telemetryID+`","turn":{"id":"turn-`+outcome+`"}}`)
		send("turn/completed", `{"threadId":"`+telemetryID+`","turn":{"id":"turn-`+outcome+`","status":"`+outcome+`"}}`)
		list, _ = live.Status(context.Background())
		if list[0].TurnStatus != outcome || list[0].State == "working" {
			t.Fatalf("%s: %#v", outcome, list)
		}
	}
	o.disconnected()
	list, _ = live.Status(context.Background())
	if list[0].State != "unknown" || list[0].TurnStatus != "" {
		t.Fatalf("disconnected: %#v", list)
	}
}

func TestCodexUsageUsesCumulativeSnapshotWithoutCountingCacheOrReasoningTwice(t *testing.T) {
	live := NewCodexLive(NewCodex(nil), nil, nil)
	o := seedTelemetry(t, live, "new-codex-usage")
	event := `{"method":"thread/tokenUsage/updated","params":{"threadId":"` + telemetryID + `","turnId":"one","tokenUsage":{"total":{"totalTokens":140,"inputTokens":100,"cachedInputTokens":30,"cacheWriteInputTokens":10,"outputTokens":40,"reasoningOutputTokens":20},"last":{"totalTokens":14,"inputTokens":10,"cachedInputTokens":3,"cacheWriteInputTokens":1,"outputTokens":4,"reasoningOutputTokens":2}}}}`
	o.server([]byte(event))
	o.server([]byte(event))
	summary, err := live.Usage(context.Background(), "new-codex-usage", `C:\work`)
	if err != nil || !summary.SessionFound || summary.ProjectFound || summary.SessionTotal.TotalTokens() != 140 || summary.SessionTotal.InputTokens != 60 || summary.Latest.TotalTokens() != 14 {
		t.Fatalf("usage: %#v %v", summary, err)
	}
	if summary.RequestCountsKnown == nil || *summary.RequestCountsKnown || summary.CacheTiersKnown == nil || *summary.CacheTiersKnown {
		t.Fatal("unknown details reported as zero", summary)
	}
	if _, err := live.Usage(context.Background(), "new-codex-other", `C:\work`); err == nil {
		t.Fatal("another runtime used snapshot")
	}
	if _, err := live.Usage(context.Background(), telemetryID, `C:\other`); err == nil {
		t.Fatal("wrong directory used snapshot")
	}
	o.server([]byte(`{"method":"thread/tokenUsage/updated","params":{"threadId":"` + telemetryID + `","tokenUsage":{"total":{"inputTokens":-1},"last":{}}}}`))
	summary, _ = live.Usage(context.Background(), telemetryID, `C:\work`)
	if summary.SessionTotal.TotalTokens() != 140 || !summary.Stale || len(summary.Warnings) == 0 {
		t.Fatal("invalid usage destroyed snapshot")
	}
}

func TestCodexResumeBindingRejectsWrongThreadAndPreservesTypedRequestID(t *testing.T) {
	live := NewCodexLive(NewCodex(nil), nil, nil)
	o := live.observe("new-codex-identity", `C:\work`)
	o.client([]byte(`{"id":1,"method":"thread/resume","params":{"threadId":"` + telemetryID + `"}}`))
	o.server([]byte(`{"id":"1","result":{"thread":{"id":"` + telemetryID + `","status":{"type":"idle"}}}}`))
	if list, _ := live.Status(context.Background()); len(list) != 0 {
		t.Fatal("string ID matched integer ID", list)
	}
	o.server([]byte(`{"id":1,"result":{"thread":{"id":"01900000-0000-7000-8000-000000000002","status":{"type":"idle"}}}}`))
	if list, _ := live.Status(context.Background()); len(list) != 0 {
		t.Fatal("wrong resumed thread bound", list)
	}
}

func TestCodexObserverIgnoresChildThreadsAndReplaysUsageBeforeResumeReply(t *testing.T) {
	live := NewCodexLive(NewCodex(nil), nil, nil)
	o := live.observe("new-codex-early", `C:\work`)
	o.client([]byte(`{"id":1,"method":"thread/resume","params":{"threadId":"` + telemetryID + `"}}`))
	o.server([]byte(`{"method":"thread/tokenUsage/updated","params":{"threadId":"` + telemetryID + `","tokenUsage":{"total":{"totalTokens":12,"inputTokens":10,"outputTokens":2},"last":{"totalTokens":12,"inputTokens":10,"outputTokens":2}}}}`))
	o.server([]byte(`{"id":1,"result":{"thread":{"id":"` + telemetryID + `","status":{"type":"idle"}}}}`))
	summary, _ := live.Usage(context.Background(), "new-codex-early", `C:\work`)
	if !summary.SessionFound || summary.SessionTotal.TotalTokens() != 12 {
		t.Fatal(summary)
	}
	o.server([]byte(`{"method":"thread/status/changed","params":{"threadId":"01900000-0000-7000-8000-000000000002","status":{"type":"active"}}}`))
	list, _ := live.Status(context.Background())
	if list[0].State != "idle" {
		t.Fatal("child changed parent", list)
	}
}
