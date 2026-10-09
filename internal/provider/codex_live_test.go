package provider

import (
	"context"
	"testing"
)

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
