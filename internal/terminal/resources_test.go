package terminal

import (
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTerminalEnvironmentReplacesCaseInsensitiveKeysWithoutDuplicates(t *testing.T) {
	env, err := terminalEnvironment([]string{"Term=dumb", "NO_Color=1", "fyne_token=old", "Path=keep"}, []string{"FYNE_TOKEN=new"})
	if err != nil || !reflect.DeepEqual(env, []string{"Path=keep", "FYNE_TOKEN=new", "TERM=xterm-256color"}) {
		t.Fatalf("env: %v %v", env, err)
	}
	for _, entry := range []string{"missing_equals", "=empty", "BAD\x00NAME=value", "KEY=value\x00"} {
		if _, err := terminalEnvironment(nil, []string{entry}); err == nil {
			t.Fatalf("accepted %q", entry)
		}
	}
}

func TestTerminalOwnedResourceClosesOnceOnReplacementKillAndShutdown(t *testing.T) {
	var cleanup atomic.Int32
	m := NewManagerWithStart(Callbacks{}, nil, func(_, _ string, _, _ int, env []string) (Pty, error) {
		found := false
		for _, entry := range env {
			if strings.HasPrefix(entry, "OWNED_TOKEN=") {
				found = true
			}
		}
		if !found {
			t.Error("owned environment missing")
		}
		return &fakePty{read: make(chan struct{})}, nil
	})
	defer m.CloseAll()
	for i := 0; i < 2; i++ {
		if err := m.StartWithOptions("one", "cmd", ".", []string{"OWNED_TOKEN=isolated-test"}, func() { cleanup.Add(1) }); err != nil {
			t.Fatal(err)
		}
	}
	if cleanup.Load() != 1 {
		t.Fatalf("replacement cleanup=%d", cleanup.Load())
	}
	m.Kill("one")
	m.Kill("one")
	if cleanup.Load() != 2 {
		t.Fatalf("kill cleanup=%d", cleanup.Load())
	}
	if err := m.StartWithOptions("two", "cmd", ".", []string{"OWNED_TOKEN=test"}, func() { cleanup.Add(1) }); err != nil {
		t.Fatal(err)
	}
	m.CloseAll()
	m.CloseAll()
	if cleanup.Load() != 3 {
		t.Fatalf("shutdown cleanup=%d", cleanup.Load())
	}
}

func TestTerminalReaderExitClosesOwnedResource(t *testing.T) {
	var cleanup atomic.Int32
	exited := make(chan struct{})
	m := NewManagerWithStart(Callbacks{Exit: func(string) { close(exited) }}, nil, func(_, _ string, _, _ int, _ []string) (Pty, error) { return &fakePty{}, nil })
	if err := m.StartWithOptions("done", "cmd", ".", nil, func() { cleanup.Add(1) }); err != nil {
		t.Fatal(err)
	}
	<-exited
	m.CloseAll()
	if cleanup.Load() != 1 {
		t.Fatal("reader cleanup count", cleanup.Load())
	}
}
