package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gp-alex-chen/claude-session-manager/internal/agent"
	"github.com/gp-alex-chen/claude-session-manager/internal/state"
)

func TestProgramEventsBaselineEdgesAndCompletion(t *testing.T) {
	d := newEventDetector()
	work := agent.AgentInfo{SessionID: "s", RuntimeToken: "runtime", Provider: "codex", State: "working", TurnID: "t1", TurnStatus: "inProgress"}
	if got := d.observe("codex", []agent.AgentInfo{work}); len(got) != 0 {
		t.Fatal("startup replay", got)
	}
	wait := work
	wait.State, wait.Status, wait.WaitingFor = "blocked", "waiting", "approval"
	got := d.observe("codex", []agent.AgentInfo{wait})
	if len(got) != 1 || got[0].Event != "permission_requested" {
		t.Fatalf("approval: %#v", got)
	}
	if len(d.observe("codex", []agent.AgentInfo{wait})) != 0 {
		t.Fatal("duplicate wait")
	}
	unknown := wait
	unknown.WaitingFor = ""
	unknown.State = "unknown"
	unknown.Status = "unknown"
	if len(d.observe("codex", []agent.AgentInfo{unknown})) != 0 || len(d.observe("codex", []agent.AgentInfo{wait})) != 0 {
		t.Fatal("unknown snapshot rearmed existing wait")
	}
	wait.SessionID = "adopted" // Runtime identity must survive native ID adoption.
	if len(d.observe("codex", []agent.AgentInfo{wait})) != 0 {
		t.Fatal("adoption replayed wait")
	}
	wait.WaitingFor = "userInput"
	if got = d.observe("codex", []agent.AgentInfo{wait}); len(got) != 1 || got[0].Event != "input_requested" {
		t.Fatalf("input: %#v", got)
	}
	d.observe("codex", []agent.AgentInfo{work})
	if len(d.observe("codex", []agent.AgentInfo{wait})) != 1 {
		t.Fatal("new wait not emitted")
	}
	done := work
	done.State, done.Status, done.TurnStatus = "idle", "idle", "completed"
	if got = d.observe("codex", []agent.AgentInfo{done}); len(got) != 1 || got[0].Event != "task_completed" {
		t.Fatalf("completion: %#v", got)
	}
	if len(d.observe("codex", []agent.AgentInfo{done})) != 0 {
		t.Fatal("duplicate completion")
	}
	failed := work
	failed.TurnID = "t2"
	failed.State = "failed"
	failed.TurnStatus = "failed"
	if len(d.observe("codex", []agent.AgentInfo{failed})) != 0 {
		t.Fatal("failure became completion")
	}
	if len(d.observe("codex", nil)) != 0 {
		t.Fatal("exit became completion")
	}
	// A provider's first snapshot remains a baseline even after another provider.
	if len(d.observe("claude", []agent.AgentInfo{wait})) != 0 {
		t.Fatal("provider baseline missing")
	}
	work.Provider = "claude"
	work.RuntimeToken = ""
	work.State = "working"
	d.observe("claude", []agent.AgentInfo{work})
	idle := work
	idle.State = "idle"
	idle.Status = "idle"
	if got = d.observe("claude", []agent.AgentInfo{idle}); len(got) != 1 || got[0].Event != "task_completed" {
		t.Fatalf("Claude completion: %#v", got)
	}
}

func receiveResult(t *testing.T, ch <-chan ProgramResult) ProgramResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not finish")
		return ProgramResult{}
	}
}

func TestProgramServiceFiltersSerializesAndTestsDisabledConfig(t *testing.T) {
	cfg := state.DefaultNotificationConfig()
	cfg.Enabled = true
	cfg.Program = "fixture.exe"
	cfg.Events = []string{"permission_requested"}
	results := make(chan ProgramResult, 8)
	inputs := make(chan ProgramEvent, 8)
	s := newProgramService(context.Background(), cfg, func(_ context.Context, c state.NotificationConfig, b []byte) ProgramResult {
		var event ProgramEvent
		if err := json.Unmarshal(b, &event); err != nil {
			t.Error(err)
		}
		inputs <- event
		return ProgramResult{Success: true, ExitCode: 0}
	}, func(r ProgramResult) { results <- r })
	defer s.Close()
	s.Observe("claude", []agent.AgentInfo{})
	wait := agent.AgentInfo{SessionID: "s", State: "blocked", WaitingFor: "approval", Name: "中文", Cwd: "project"}
	s.Observe("claude", []agent.AgentInfo{wait})
	r := receiveResult(t, results)
	e := <-inputs
	if !r.Success || e.EventID == "" || e.SchemaVersion != 1 || e.SessionName != "中文" || e.Test {
		t.Fatalf("payload: %#v %#v", e, r)
	}
	if err := s.Test("task_completed"); err != nil {
		t.Fatal(err)
	}
	if !receiveResult(t, results).Success || !(<-inputs).Test {
		t.Fatal("test event missing")
	}
	cfg.Enabled = false
	s.Configure(cfg)
	if err := s.Test("permission_requested"); err != nil {
		t.Fatal(err)
	}
	receiveResult(t, results)
	<-inputs
	s.Observe("claude", []agent.AgentInfo{})
	s.Observe("claude", []agent.AgentInfo{wait})
	if s.Status().Pending != 0 {
		t.Fatal("disabled notification queued")
	}
}

func TestProgramServiceConfigurationCancelsActiveAndDropsQueuedJobs(t *testing.T) {
	cfg := state.DefaultNotificationConfig()
	cfg.Program = "fixture.exe"
	cfg.Enabled = true
	started := make(chan state.NotificationConfig, 2)
	results := make(chan ProgramResult, 8)
	s := newProgramService(context.Background(), cfg, func(ctx context.Context, c state.NotificationConfig, _ []byte) ProgramResult {
		started <- c
		<-ctx.Done()
		return ProgramResult{Error: ctx.Err().Error(), ExitCode: -1}
	}, func(r ProgramResult) { results <- r })
	if err := s.Test("input_requested"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	for i := 0; i < programQueueLimit; i++ {
		if err := s.Test("input_requested"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Test("input_requested"); err == nil {
		t.Fatal("unbounded queue")
	}
	next := cfg
	next.Program = "new.exe"
	s.Configure(next)
	receiveResult(t, results)
	if s.Status().Pending != 0 {
		t.Fatal("old jobs survived configuration change")
	}
	s.Close()
	if err := s.Test("input_requested"); err == nil {
		t.Fatal("closed service accepted event")
	}
}

func TestProgramCompletionRejectsAmbiguousFailuresAndPreservesTurnDeduplication(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		for _, bad := range []agent.AgentInfo{
			{State: "blocked", Status: "waiting"}, {State: "blocked", Status: "idle"},
			{State: "unknown"}, {State: "failed"}, {State: "interrupted"}, {State: "queued"},
		} {
			d := newEventDetector()
			work := agent.AgentInfo{SessionID: "s", State: "working", TurnID: "t", TurnStatus: "inProgress"}
			d.observe(provider, []agent.AgentInfo{work})
			bad.SessionID = "s"
			bad.TurnID = "t"
			bad.TurnStatus = "completed"
			if got := d.observe(provider, []agent.AgentInfo{bad}); len(got) != 0 {
				t.Fatalf("%s %#v produced %#v", provider, bad, got)
			}
		}
	}
	d := newEventDetector()
	a := agent.AgentInfo{SessionID: "s", State: "idle", Status: "idle", TurnID: "first", TurnStatus: "completed"}
	if len(d.observe("codex", []agent.AgentInfo{a})) != 0 {
		t.Fatal("baseline completed turn replayed")
	}
	a.State = "working"
	a.TurnStatus = "inProgress"
	a.TurnID = "second"
	d.observe("codex", []agent.AgentInfo{a})
	a.State = "idle"
	a.TurnStatus = "completed"
	if len(d.observe("codex", []agent.AgentInfo{a})) != 1 {
		t.Fatal("new turn lost")
	}
	a.State = "working"
	d.observe("codex", []agent.AgentInfo{a})
	a.State = "idle"
	if len(d.observe("codex", []agent.AgentInfo{a})) != 0 {
		t.Fatal("same turn replayed after incidental state change")
	}
}

func TestProgramServiceRunsOneJobAtATimeInAcceptanceOrder(t *testing.T) {
	c := state.DefaultNotificationConfig()
	c.Program = "fixture.exe"
	started := make(chan string, 3)
	release := make(chan struct{})
	results := make(chan ProgramResult, 3)
	s := newProgramService(context.Background(), c, func(ctx context.Context, c state.NotificationConfig, b []byte) ProgramResult {
		var e ProgramEvent
		_ = json.Unmarshal(b, &e)
		started <- e.Event
		select {
		case <-release:
		case <-ctx.Done():
		}
		return ProgramResult{Success: true}
	}, func(r ProgramResult) { results <- r })
	defer s.Close()
	for _, event := range []string{"input_requested", "permission_requested", "task_completed"} {
		if err := s.Test(event); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case event := <-started:
		if event != "input_requested" {
			t.Fatal(event)
		}
	case <-time.After(time.Second):
		t.Fatal("worker absent")
	}
	if s.Status().Pending != 2 || !s.Status().Running {
		t.Fatalf("not serialized: %#v", s.Status())
	}
	select {
	case <-started:
		t.Fatal("concurrent runner")
	default:
	}
	release <- struct{}{}
	receiveResult(t, results)
	select {
	case event := <-started:
		if event != "permission_requested" {
			t.Fatal(event)
		}
	case <-time.After(time.Second):
		t.Fatal("second job absent")
	}
	release <- struct{}{}
	receiveResult(t, results)
	select {
	case event := <-started:
		if event != "task_completed" {
			t.Fatal(event)
		}
	case <-time.After(time.Second):
		t.Fatal("third job absent")
	}
	release <- struct{}{}
	receiveResult(t, results)
}

func TestProgramRunnerInputArgsDirectoryFailureAndCancellation(t *testing.T) {
	// Subprocess tests use this test executable, never an installed interpreter or user data.
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	output := filepath.Join(dir, "received.json")
	c := state.DefaultNotificationConfig()
	c.Program = program
	c.WorkingDir = dir
	c.Args = []string{"-test.run=^TestNotificationProgramHelper$", "--", "--notify-helper", output, "空 格 & $()"}
	payload := []byte(`{"event":"permission_requested","session_name":"中文"}`)
	result := runProgram(context.Background(), c, payload)
	if !result.Success || result.ExitCode != 0 {
		t.Fatalf("run: %#v", result)
	}
	b, err := os.ReadFile(output)
	if err != nil || string(b) != string(payload) {
		t.Fatalf("stdin: %q %v", b, err)
	}
	c.Args[3] = "fail"
	result = runProgram(context.Background(), c, payload)
	if result.Success || result.ExitCode != 7 || len(result.Error) > programErrorLimit || !strings.Contains(result.Error, "fixture failure") {
		t.Fatalf("failure: %#v", result)
	}
	c.Args[3] = "block"
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	result = runProgram(ctx, c, payload)
	if result.Success || !strings.Contains(result.Error, "超时") {
		t.Fatalf("timeout: %#v", result)
	}
	c.Program = filepath.Join(dir, "missing.exe")
	if result = runProgram(context.Background(), c, payload); result.Success || result.ExitCode != -1 {
		t.Fatalf("missing: %#v", result)
	}
}

func TestNotificationProgramHelper(t *testing.T) {
	index := -1
	for i, arg := range os.Args {
		if arg == "--notify-helper" {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	mode := os.Args[index+1]
	if mode == "fail" {
		fmt.Fprint(os.Stderr, "fixture failure "+strings.Repeat("x", programErrorLimit*8))
		os.Exit(7)
	}
	if mode == "block" {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		<-ticker.C
		os.Exit(0)
	}
	if os.Args[index+2] != "空 格 & $()" {
		os.Exit(8)
	}
	cwd, _ := os.Getwd()
	if filepath.Clean(cwd) != filepath.Dir(mode) {
		os.Exit(9)
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(10)
	}
	if os.WriteFile(mode, b, 0600) != nil {
		os.Exit(11)
	}
	os.Exit(0)
}
