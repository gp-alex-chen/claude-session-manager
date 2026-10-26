package provider

import (
	"context"
	"testing"

	"github.com/gp-alex-chen/claude-session-manager/internal/state"
)

func TestNativeCatalogShutdownPersistStartupRestoreDoesNotRestoreGrants(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(dir)
	p := NewCodexLive(NewCodex(referenceHistory{}), nil, nil)
	p.nativeTools = true
	p.nativeToolState = store
	o := p.observe("before-shutdown", "fixture")
	o.relay, _ = testRelay(t)
	dialRelay(t, o.relay)
	o.generation = 1
	start, err := registerNativeReferenceTools([]byte(`{"id":1,"method":"thread/start","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	o.client(start)
	o.server([]byte(`{"id":1,"result":{"thread":{"id":"` + telemetryID + `","status":{"type":"idle"}}}}`))
	if !o.nativeTools {
		t.Fatal("successful native tool registration not confirmed")
	}
	o.references = map[string]SessionReference{"ref_old": {Reference: "ref_old", SessionID: "01900000-0000-7000-8000-000000000002"}}
	p.SealRestoration()
	if ids, err := p.RestorableSessions(context.Background(), true); err != nil || len(ids) != 1 {
		t.Fatal(ids, err)
	}
	o.finished()
	p.CloseService()
	restored := NewSharedCodexLive(func(string) (string, error) { t.Fatal("unit test started a CLI"); return "", nil }, nil, state.NewStore(dir))
	defer restored.CloseService()
	current := restored.observe("after-startup", "fixture")
	current.relay, _ = testRelay(t)
	dialRelay(t, current.relay)
	current.generation = 2
	current.client([]byte(`{"id":2,"method":"thread/resume","params":{"threadId":"` + telemetryID + `"}}`))
	current.server([]byte(`{"id":2,"result":{"thread":{"id":"` + telemetryID + `","status":{"type":"idle"}}}}`))
	if !current.nativeTools || len(current.references) != 0 {
		t.Fatal("native capability was lost or authorization was restored")
	}
	values, err := state.NewStore(dir).LoadCodexNativeTools()
	if err != nil || len(values) != 1 || values[telemetryID] != nativeReferenceCatalog {
		t.Fatal(values, err)
	}
}
