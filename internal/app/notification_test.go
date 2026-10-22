package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gp-alex-chen/claude-session-manager/internal/agent"
	"github.com/gp-alex-chen/claude-session-manager/internal/notify"
	"github.com/gp-alex-chen/claude-session-manager/internal/provider"
	"github.com/gp-alex-chen/claude-session-manager/internal/state"
)

func TestNotificationShutdownPersistStartupRestoreAndGetterDoesNotDeliver(t *testing.T) {
	a, _, logs, dir := testApp(t)
	cfg := state.DefaultNotificationConfig()
	cfg.Enabled = true
	cfg.Program = filepath.Join(dir, "missing-program.exe")
	cfg.Args = []string{"中文 参数"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.ctx = ctx
	a.cancelLifecycle = cancel
	a.notificationProgram = notify.NewProgramService(ctx, state.DefaultNotificationConfig())
	if err := a.SetNotificationConfig(cfg); err != nil {
		t.Fatal(err)
	}
	claude, _ := a.providers.Lookup(provider.Claude)
	claude.(*provider.ClaudeAdapter).GetStatus = func(context.Context) []agent.AgentInfo {
		return []agent.AgentInfo{{SessionID: "01900000-0000-7000-8000-000000000001", State: "blocked", Status: "waiting", WaitingFor: "approval"}}
	}
	_ = a.GetAgents()
	_ = a.GetAgents()
	if status := a.GetNotificationStatus(); status.Pending != 0 || status.Running || status.Last != nil {
		t.Fatalf("getter delivered: %#v", status)
	}
	a.observeNotificationSource(ctx, "claude", []agent.AgentInfo{})
	a.observeNotificationSource(ctx, "claude", []agent.AgentInfo{{SessionID: "s", State: "blocked", WaitingFor: "approval"}})
	status := a.GetNotificationStatus()
	if status.Pending == 0 && !status.Running && status.Last == nil {
		t.Fatal("observed approval was not accepted")
	}
	a.Shutdown(context.Background())
	if err := a.TestNotification("permission_requested"); err == nil {
		t.Fatal("shutdown accepted work")
	}
	if len(*logs) != 0 {
		t.Fatalf("notifications wrote logs: %v", *logs)
	}
	restarted := NewAppWithStore(state.NewStore(dir))
	restarted.providers = restarted.newProviderRegistry(false)
	stoppedCtx, stop := context.WithCancel(context.Background())
	stop()
	restarted.Startup(stoppedCtx)
	defer restarted.Shutdown(context.Background())
	got, err := restarted.GetNotificationConfig()
	if err != nil || !reflect.DeepEqual(got, cfg) {
		t.Fatalf("restore: %#v %v", got, err)
	}
	if status := restarted.GetNotificationStatus(); status.Last != nil || status.Pending != 0 {
		t.Fatal("replayed results on restart")
	}
}

func TestNotificationInvalidSaveAndCorruptionCannotEnableExecution(t *testing.T) {
	a, _, _, dir := testApp(t)
	cfg := state.DefaultNotificationConfig()
	if err := a.SetNotificationConfig(cfg); err != nil {
		t.Fatal(err)
	}
	invalid := cfg
	invalid.Enabled = true
	if err := a.SetNotificationConfig(invalid); err == nil {
		t.Fatal("enabled without program")
	}
	if got, _ := a.GetNotificationConfig(); got.Enabled {
		t.Fatal("failed save enabled notifications")
	}
	if err := os.WriteFile(filepath.Join(dir, "notification.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.GetNotificationConfig(); err == nil {
		t.Fatal("corrupt config hidden")
	}
	if err := a.SetNotificationConfig(cfg); err == nil {
		t.Fatal("overwrote corrupt file")
	}
}
