package provider

import (
	"context"
	"testing"
)

func TestSessionKeysPreserveClaudeAndSeparateProviders(t *testing.T) {
	id := "01900000-0000-7000-8000-000000000001"
	for _, ref := range []SessionRef{{Claude, id}, {Codex, id}} {
		key, err := EncodeKey(ref)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeKey(key)
		if err != nil || got != ref {
			t.Fatalf("round trip %q: %#v %v", key, got, err)
		}
		if ref.Provider == Claude && key != id {
			t.Fatalf("legacy key changed: %q", key)
		}
	}
	for _, key := range []string{"codex:bad-id", "bad provider:abc", "codex:01900000-0000-7000-8000-000000000001&whoami", "new-abc"} {
		if _, err := DecodeKey(key); err == nil {
			t.Fatalf("accepted %q", key)
		}
	}
}

func TestSessionCodecAllowsFutureProvidersWithoutChangingExistingKeys(t *testing.T) {
	ref := SessionRef{Provider: "another-agent", ID: "session/with space"}
	key, err := EncodeKey(ref)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeKey(key)
	if err != nil || got != ref {
		t.Fatalf("extension codec: %q %#v %v", key, got, err)
	}
	r := NewRegistry()
	var nilAdapter *CodexAdapter
	if err := r.Register(nilAdapter); err == nil {
		t.Fatal("accepted typed nil")
	}
}

func TestCodexLaunchNeverPassesInternalKeyOrBypassesPermissions(t *testing.T) {
	p := NewCodex(nil)
	id := "01900000-0000-7000-8000-000000000001"
	spec, err := p.PrepareLaunch(context.Background(), OpenRequest{Ref: &SessionRef{Codex, id}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Command != "codex" || len(spec.Args) != 3 || spec.Args[0] != "resume" || spec.Args[1] != id || spec.Args[2] != "--no-daemon" {
		t.Fatalf("resume: %#v", spec)
	}
	if spec.Identity != nil && *spec.Identity != (SessionRef{Codex, id}) {
		t.Fatalf("identity: %#v", spec.Identity)
	}
	if _, err := p.PrepareLaunch(context.Background(), OpenRequest{Ref: &SessionRef{Claude, id}}); err == nil {
		t.Fatal("accepted wrong provider")
	}
	if _, err := p.PrepareLaunch(context.Background(), OpenRequest{Ref: &SessionRef{Codex, "abc&whoami"}}); err == nil {
		t.Fatal("accepted unsafe ID")
	}
	spec, err = p.PrepareLaunch(context.Background(), OpenRequest{})
	if err != nil || len(spec.Args) != 1 || spec.Args[0] != "--no-daemon" || spec.Identity != nil {
		t.Fatalf("new: %#v %v", spec, err)
	}
}

func TestRegistryRejectsDuplicateAndUnknownProviders(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(NewCodex(nil)); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(NewCodex(nil)); err == nil {
		t.Fatal("accepted duplicate")
	}
	if _, err := r.Lookup("other"); err == nil {
		t.Fatal("accepted unknown provider")
	}
	d := r.Descriptors()
	if len(d) != 1 || d[0].ID != Codex || d[0].HasUsage || d[0].HasStatus {
		t.Fatalf("capabilities: %#v", d)
	}
}
