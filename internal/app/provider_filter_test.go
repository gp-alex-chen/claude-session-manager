package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gp-alex-chen/claude-session-manager/internal/provider"
	"github.com/gp-alex-chen/claude-session-manager/internal/session"
)

type countedFilterHistory struct {
	calls int
	err   error
	rows  []provider.Session
}

func (h *countedFilterHistory) ListSessions(context.Context) ([]provider.Session, error) {
	h.calls++
	return h.rows, h.err
}

func TestFilteredHistorySkipsUnselectedAdaptersAndFiltersArchive(t *testing.T) {
	a, store, _, _ := testApp(t)
	id := "01900000-0000-7000-8000-000000000001"
	h := &countedFilterHistory{rows: []provider.Session{{Ref: provider.SessionRef{Provider: provider.Codex, ID: id}, Dir: "fixture", Name: "Codex"}}}
	if err := a.providers.Register(provider.NewCodex(h)); err != nil {
		t.Fatal(err)
	}
	claudeCalls := 0
	p, _ := a.providers.Lookup(provider.Claude)
	p.(*provider.ClaudeAdapter).History = func() ([]*session.Session, error) {
		claudeCalls++
		return []*session.Session{{ID: id, Dir: "fixture", Name: "Claude"}}, nil
	}
	list, err := a.ListSessionsForProviders([]string{"claude", "claude"})
	if err != nil || len(list.Sessions) != 1 || list.Sessions[0].Provider != "claude" || h.calls != 0 || claudeCalls != 1 || !reflect.DeepEqual(list.SucceededProviders, []string{"claude"}) {
		t.Fatalf("query=%+v calls=%d/%d err=%v", list, claudeCalls, h.calls, err)
	}
	if err := store.SetHidden(id, true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetHidden("codex:"+id, true); err != nil {
		t.Fatal(err)
	}
	hidden, err := a.ListHiddenSessionsForProviders([]string{"claude"})
	if err != nil || len(hidden.Sessions) != 1 || hidden.Sessions[0].ID != id || h.calls != 0 {
		t.Fatalf("archive=%+v err=%v", hidden, err)
	}
	for _, ids := range [][]string{nil, {}, {"unknown"}, {"claude", "unknown"}} {
		if _, err := a.ListSessionsForProviders(ids); err == nil {
			t.Fatalf("accepted providers=%v", ids)
		}
	}
	if claudeCalls != 2 || h.calls != 0 {
		t.Fatal("invalid selection invoked adapters")
	}
}

func TestFilteredHistoryFailureIsNotReportedAsAnEmptySuccessfulBaseline(t *testing.T) {
	a, _, _, _ := testApp(t)
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	a.sessionCatalog = session.NewCatalog(path)
	for i := 0; i < 2; i++ {
		result, err := a.ListSessionsForProviders([]string{"claude"})
		if err != nil || len(result.SucceededProviders) != 0 || len(result.Warnings) == 0 {
			t.Fatalf("unsafe baseline=%+v err=%v", result, err)
		}
	}
	h := &countedFilterHistory{err: errors.New("Codex unavailable")}
	if err := a.providers.Register(provider.NewCodex(h)); err != nil {
		t.Fatal(err)
	}
	result, err := a.ListSessionsForProviders([]string{"codex"})
	if err != nil || len(result.SucceededProviders) != 0 || len(result.Warnings) != 1 {
		t.Fatalf("failed provider=%+v err=%v", result, err)
	}
}

func TestOpenSessionRefsDecodeProviderIdentityAndSkipInvalidKeys(t *testing.T) {
	a, store, logs, _ := testApp(t)
	if err := a.providers.Register(provider.NewCodex(nil)); err != nil {
		t.Fatal(err)
	}
	id := "01900000-0000-7000-8000-000000000001"
	if err := store.SaveOpen([]string{id, "codex:" + id, "unknown:abc", "codex:invalid", "claude:" + id}); err != nil {
		t.Fatal(err)
	}
	refs := a.GetOpenSessionRefs()
	want := []OpenSessionRef{{ID: id, Provider: "claude"}, {ID: "codex:" + id, Provider: "codex"}}
	if !reflect.DeepEqual(refs, want) || !containsLog(*logs, "忽略无效恢复会话") {
		t.Fatalf("refs=%v logs=%v", refs, *logs)
	}
}
