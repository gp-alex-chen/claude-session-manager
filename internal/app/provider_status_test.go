package app

import (
	"context"
	"errors"
	"testing"

	"github.com/gp-alex-chen/claude-session-manager/internal/agent"
	"github.com/gp-alex-chen/claude-session-manager/internal/provider"
)

type countedStatusProvider struct {
	provider.ClaudeAdapter
	id    provider.ID
	reads int
	err   error
}

func (p *countedStatusProvider) Descriptor() provider.Descriptor {
	return provider.Descriptor{ID: p.id}
}

func (p *countedStatusProvider) Status(context.Context) ([]agent.AgentInfo, error) {
	p.reads++
	return []agent.AgentInfo{{SessionID: "01900000-0000-7000-8000-000000000001", State: "idle"}}, p.err
}

func TestGetAgentsReadsEachProviderOnceAndPreservesNativeWarnings(t *testing.T) {
	a, _, logs, _ := testApp(t)
	a.providers = provider.NewRegistry()
	sources := []*countedStatusProvider{{id: provider.Claude}, {id: provider.Codex}, {id: provider.Antigravity}}
	for _, source := range sources {
		if err := a.providers.Register(source); err != nil {
			t.Fatal(err)
		}
	}
	list := a.GetAgents()
	if len(list) != len(sources) {
		t.Fatal("provider status missing", list)
	}
	for i, source := range sources {
		if source.reads != 1 || list[i].Provider != string(source.id) {
			t.Errorf("%s status reads=%d, result=%#v", source.id, source.reads, list[i])
		}
	}
	sources[2].err = errors.New("synthetic snapshot error")
	if list := a.GetAgents(); len(list) != 2 || len(*logs) != 1 {
		t.Fatal("native error must remain visible without stale status", list, *logs)
	}
}
