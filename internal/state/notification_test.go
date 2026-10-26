package state

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNotificationConfigDefaultsValidationAndRestart(t *testing.T) {
	store := NewStore(t.TempDir())
	cfg, err := store.NotificationConfig()
	if err != nil || cfg.Enabled || cfg.TimeoutSeconds != 10 || len(cfg.Events) != 3 {
		t.Fatalf("defaults: %#v, %v", cfg, err)
	}
	cfg.Enabled, cfg.Program, cfg.Args = true, "python.exe", []string{"script with spaces.py", "中文"}
	if err := store.SaveNotificationConfig(cfg); err != nil {
		t.Fatal(err)
	}
	restored, err := NewStore(store.dir).NotificationConfig()
	if err != nil || !reflect.DeepEqual(cfg, restored) {
		t.Fatalf("restart: %#v %v", restored, err)
	}
	for _, change := range []func(*NotificationConfig){
		func(c *NotificationConfig) { c.Program = "" },
		func(c *NotificationConfig) { c.TimeoutSeconds = 121 },
		func(c *NotificationConfig) { c.Events = []string{"unknown"} },
		func(c *NotificationConfig) { c.Args = []string{"bad\x00arg"} },
	} {
		bad := cfg
		change(&bad)
		if err := store.SaveNotificationConfig(bad); err == nil {
			t.Fatal("invalid config saved")
		}
	}
	after, _ := store.NotificationConfig()
	if !reflect.DeepEqual(cfg, after) {
		t.Fatal("invalid save changed config")
	}
}

func TestCorruptNotificationConfigIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notification.json")
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(dir)
	if _, err := store.NotificationConfig(); err == nil {
		t.Fatal("corruption hidden")
	}
	if err := store.SaveNotificationConfig(DefaultNotificationConfig()); err == nil {
		t.Fatal("overwrote corrupt config")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "{" {
		t.Fatal("corrupt data replaced")
	}
}

func TestAttentionSubscriptionIsOptInAndSurvivesRestart(t *testing.T) {
	store := NewStore(t.TempDir())
	cfg := DefaultNotificationConfig()
	if len(cfg.Events) != 3 {
		t.Fatal("new event silently enabled for old scripts")
	}
	cfg.Enabled, cfg.Program = true, "fixture.exe"
	cfg.Events = append(cfg.Events, "attention_changed")
	if err := store.SaveNotificationConfig(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := NewStore(store.dir).NotificationConfig()
	if err != nil || !reflect.DeepEqual(cfg, got) {
		t.Fatalf("restore: %#v %v", got, err)
	}
	cfg.Events = append(cfg.Events, "attention_changed")
	if err := store.SaveNotificationConfig(cfg); err == nil {
		t.Fatal("duplicate event accepted")
	}
}
