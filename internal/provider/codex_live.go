package provider

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gp-alex-chen/claude-session-manager/internal/agent"
	"github.com/gp-alex-chen/claude-session-manager/internal/usage"
)

// CodexRelayFactory starts a terminal-owned official server and a transparent
// local transport. Observers must never answer or change a protocol message.
type CodexRelayFactory func(context.Context, string, func([]byte), func([]byte), func()) (LaunchSpec, error)

type CodexLive struct {
	*CodexAdapter
	open     CodexRelayFactory
	changed  func()
	mu       sync.Mutex
	records  map[string]*codexObservation
	sequence uint64
}

func NewCodexLive(base *CodexAdapter, open CodexRelayFactory, changed func()) *CodexLive {
	return &CodexLive{CodexAdapter: base, open: open, changed: changed, records: make(map[string]*codexObservation)}
}

func (p *CodexLive) Descriptor() Descriptor {
	d := p.CodexAdapter.Descriptor()
	d.NewSessionRestorable = true
	return d
}

func (p *CodexLive) PrepareLaunch(ctx context.Context, req OpenRequest) (LaunchSpec, error) {
	base, err := p.CodexAdapter.PrepareLaunch(ctx, req)
	if err != nil {
		return LaunchSpec{}, err
	}
	if req.RuntimeToken == "" {
		return base, nil
	}
	if p.open == nil {
		return LaunchSpec{}, errors.New("Codex live transport unavailable")
	}
	o := p.observe(req.RuntimeToken, req.Dir)
	spec, err := p.open(ctx, req.Dir, o.client, o.server, o.disconnected)
	if err != nil {
		o.finished()
		return LaunchSpec{}, err
	}
	if req.Ref != nil {
		spec.Args = append(spec.Args, "resume", req.Ref.ID)
		spec.Identity = base.Identity
	}
	closeRelay := spec.Close
	var closeOnce sync.Once
	spec.Close = func() {
		closeOnce.Do(func() {
			if closeRelay != nil {
				closeRelay()
			}
			o.finished()
		})
	}
	return spec, nil
}

func (p *CodexLive) observe(token, dir string) *codexObservation {
	o := &codexObservation{owner: p, token: token, dir: dir, active: true, state: "unknown", pending: make(map[string]codexIdentityRequest)}
	p.mu.Lock()
	p.sequence++
	o.sequence = p.sequence
	p.records[token] = o
	if len(p.records) > 1024 {
		var oldest *codexObservation
		for _, candidate := range p.records {
			candidate.mu.Lock()
			inactive := !candidate.active
			candidate.mu.Unlock()
			if inactive && (oldest == nil || candidate.sequence < oldest.sequence) {
				oldest = candidate
			}
		}
		if oldest != nil {
			delete(p.records, oldest.token)
		}
	}
	p.mu.Unlock()
	return o
}
func (p *CodexLive) notify() {
	if p.changed != nil {
		p.changed()
	}
}

func (p *CodexLive) SealRestoration() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, o := range p.records {
		o.mu.Lock()
		o.closing = true
		o.mu.Unlock()
	}
}

func (p *CodexLive) RestorableSessions(ctx context.Context, force bool) ([]string, error) {
	p.mu.Lock()
	records := make([]*codexObservation, 0, len(p.records))
	for _, o := range p.records {
		records = append(records, o)
	}
	p.mu.Unlock()
	needsHistory := false
	for _, o := range records {
		o.mu.Lock()
		needsHistory = needsHistory || (o.active && o.id != "" && !o.saved)
		o.mu.Unlock()
	}
	var history []Session
	var err error
	if needsHistory {
		if force {
			p.invalidateHistory()
		}
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		history, err = p.ListSessions(ctx)
	}
	ids := map[string]bool{}
	for _, s := range history {
		if s.Ref.Provider == Codex {
			ids[s.Ref.ID] = true
		}
	}
	out := []string{}
	for _, o := range records {
		o.mu.Lock()
		if o.active && o.id != "" {
			if err == nil && ids[o.id] {
				o.saved = true
			}
			if o.saved {
				out = append(out, o.id)
			}
		}
		o.mu.Unlock()
	}
	return out, err
}

func (p *CodexLive) Status(context.Context) ([]agent.AgentInfo, error) {
	p.mu.Lock()
	records := make([]*codexObservation, 0, len(p.records))
	for _, o := range p.records {
		records = append(records, o)
	}
	p.mu.Unlock()
	var out []agent.AgentInfo
	for _, o := range records {
		o.mu.Lock()
		if o.active && o.id != "" {
			out = append(out, agent.AgentInfo{SessionID: o.id, RuntimeToken: o.token, Name: o.name, Provider: string(Codex), Kind: "interactive", State: o.state, Status: o.status, WaitingFor: o.waiting, TurnID: o.turnID, TurnStatus: o.turnStatus, Cwd: o.dir})
		}
		o.mu.Unlock()
	}
	return out, nil
}

func (p *CodexLive) Usage(_ context.Context, id, dir string) (usage.Summary, error) {
	p.mu.Lock()
	records := make([]*codexObservation, 0, len(p.records))
	for _, o := range p.records {
		records = append(records, o)
	}
	p.mu.Unlock()
	var result usage.Summary
	found := false
	var selectedSequence uint64
	for _, o := range records {
		o.mu.Lock()
		matches := o.token == id || o.token == "codex:"+id || o.id == id
		if matches && (dir == "" || strings.EqualFold(filepath.Clean(dir), filepath.Clean(o.dir))) && o.total != nil {
			if !found || o.sequence > selectedSequence {
				no := false
				result = usage.Summary{Provider: string(Codex), SessionFound: true, SessionTotal: *o.total, Latest: o.last, RequestCountsKnown: &no, CacheTiersKnown: &no, Note: "当前会话累计；项目汇总与请求次数暂无数据", Stale: !o.connected || o.usageWarning != ""}
				if o.usageWarning != "" {
					result.Warnings = []string{o.usageWarning}
				}
				if o.last != nil {
					copy := *o.last
					result.Latest = &copy
				}
				found = true
				selectedSequence = o.sequence
			}
		}
		o.mu.Unlock()
	}
	if !found {
		return usage.Summary{}, errors.New("Codex 当前会话尚无 Token 用量数据；打开会话后等待用量更新")
	}
	return result, nil
}

type codexObservation struct {
	closing                                                          bool
	owner                                                            *CodexLive
	sequence                                                         uint64
	mu                                                               sync.Mutex
	token, dir, id, name, state, status, waiting, turnID, turnStatus string
	active, connected                                                bool
	saved, saveHint                                                  bool
	pending                                                          map[string]codexIdentityRequest
	identitySequence, appliedIdentitySequence                        uint64
	early                                                            [][]byte
	total, last                                                      *usage.Usage
	usageWarning                                                     string
}
type codexIdentityRequest struct {
	expected string
	sequence uint64
}
type observedMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
}

func (o *codexObservation) client(data []byte) {
	var m observedMessage
	if json.Unmarshal(data, &m) != nil {
		return
	}
	if m.Method != "thread/start" && m.Method != "thread/resume" {
		return
	}
	if len(m.ID) == 0 || string(m.ID) == "null" {
		return
	}
	var params struct{ ThreadID string }
	if m.Method == "thread/resume" && (json.Unmarshal(m.Params, &params) != nil || validateRef(SessionRef{Codex, params.ThreadID}) != nil) {
		return
	}
	o.mu.Lock()
	if len(o.pending) < 64 {
		if o.closing {
			o.mu.Unlock()
			return
		}
		o.identitySequence++
		o.pending[string(m.ID)] = codexIdentityRequest{params.ThreadID, o.identitySequence}
	}
	o.mu.Unlock()
}
func (o *codexObservation) server(data []byte) {
	var m observedMessage
	if json.Unmarshal(data, &m) != nil {
		return
	}
	o.mu.Lock()
	changed := false
	if o.closing {
		o.mu.Unlock()
		return
	}
	request, pending := o.pending[string(m.ID)]
	expected := request.expected
	if len(m.ID) > 0 && m.Method == "" && pending {
		delete(o.pending, string(m.ID))
		var result struct {
			Thread struct {
				ID             string
				Cwd            string
				Name           string
				Ephemeral      bool
				ParentThreadID *string
				Status         codexThreadStatus
			}
		}
		if json.Unmarshal(m.Result, &result) == nil && !result.Thread.Ephemeral && request.sequence >= o.appliedIdentitySequence && validateRef(SessionRef{Codex, result.Thread.ID}) == nil && result.Thread.ParentThreadID == nil && (expected == "" || expected == result.Thread.ID) {
			o.appliedIdentitySequence = request.sequence
			if o.id != result.Thread.ID {
				o.saved = false
				o.total = nil
				o.last = nil
				o.turnID = ""
				o.turnStatus = ""
				o.usageWarning = ""
			}
			o.id = result.Thread.ID
			if expected != "" && !result.Thread.Ephemeral {
				o.saved = true
			}
			o.name = strings.TrimSpace(result.Thread.Name)
			o.connected = true
			o.applyStatus(result.Thread.Status)
			changed = true
			for _, early := range o.early {
				var e observedMessage
				if json.Unmarshal(early, &e) == nil {
					o.applyEvent(e)
				}
			}
		}
		o.early = nil
	} else if m.Method != "" && len(m.ID) == 0 {
		if len(o.pending) > 0 && isTelemetryEvent(m.Method) && len(o.early) < 64 && len(data) < 64<<10 {
			o.early = append(o.early, append([]byte(nil), data...))
		}
		changed = o.applyEvent(m)
	}
	o.mu.Unlock()
	if o.takeSaveHint() {
		o.owner.invalidateHistory()
	}
	if changed {
		o.owner.notify()
	}
}

func (o *codexObservation) takeSaveHint() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	hint := o.saveHint
	o.saveHint = false
	return hint
}

func isTelemetryEvent(method string) bool {
	return method == "thread/status/changed" || method == "thread/tokenUsage/updated" || method == "turn/started" || method == "turn/completed"
}

type codexThreadStatus struct {
	Type        string
	ActiveFlags []string
}

func (o *codexObservation) applyStatus(s codexThreadStatus) {
	o.waiting = ""
	o.status = "idle"
	switch s.Type {
	case "idle":
		o.state = "idle"
	case "active":
		o.state = "working"
		o.status = "busy"
		for _, flag := range s.ActiveFlags {
			if flag == "waitingOnApproval" || flag == "waitingOnUserInput" {
				o.state = "blocked"
				o.status = "waiting"
				if flag == "waitingOnApproval" {
					o.waiting = "approval"
					break
				}
				o.waiting = "userInput"
			}
		}
	case "systemError":
		o.state = "failed"
	default:
		o.state = "unknown"
		o.status = "unknown"
	}
}
func (o *codexObservation) applyEvent(m observedMessage) bool {
	var params struct {
		ThreadID   string
		Status     codexThreadStatus
		Turn       struct{ ID, Status string }
		TokenUsage *struct{ Total, Last codexTokens }
	}
	if json.Unmarshal(m.Params, &params) != nil || o.id == "" || params.ThreadID != o.id {
		return false
	}
	switch m.Method {
	case "thread/status/changed":
		o.applyStatus(params.Status)
	case "turn/started":
		o.saveHint = !o.saved
		o.state = "working"
		o.status = "busy"
		o.waiting = ""
		o.turnID = params.Turn.ID
		o.turnStatus = "inProgress"
	case "turn/completed":
		if params.Turn.ID == "" || (o.turnID != "" && o.turnID != params.Turn.ID) {
			return false
		}
		switch params.Turn.Status {
		case "completed":
			o.state = "idle"
		case "failed":
			o.state = "failed"
		case "interrupted":
			o.state = "interrupted"
		default:
			return false
		}
		o.turnID = params.Turn.ID
		o.turnStatus = params.Turn.Status
		o.saveHint = !o.saved
		o.status = "idle"
		o.waiting = ""
	case "thread/tokenUsage/updated":
		if params.TokenUsage == nil {
			o.usageWarning = "Codex 用量更新缺失数据"
			return true
		}
		total, ok := params.TokenUsage.Total.convert()
		if !ok {
			o.usageWarning = "Codex 累计用量字段不一致"
			return true
		}
		last, ok := params.TokenUsage.Last.convert()
		if !ok {
			o.usageWarning = "Codex 最近用量字段不一致"
			return true
		}
		o.total = &total
		o.last = &last
		o.usageWarning = ""
	default:
		return false
	}
	return true
}

type codexTokens struct {
	TotalTokens, InputTokens, CachedInputTokens, CacheWriteInputTokens, OutputTokens, ReasoningOutputTokens int64
	present                                                                                                 bool
}

func (t *codexTokens) UnmarshalJSON(data []byte) error {
	type fields codexTokens
	var value fields
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var required struct {
		Total  *int64 `json:"totalTokens"`
		Input  *int64 `json:"inputTokens"`
		Output *int64 `json:"outputTokens"`
	}
	if err := json.Unmarshal(data, &required); err != nil {
		return err
	}
	*t = codexTokens(value)
	t.present = required.Total != nil && required.Input != nil && required.Output != nil
	return nil
}

func (t codexTokens) convert() (usage.Usage, bool) {
	const maxExact = 1<<53 - 1
	if !t.present || t.InputTokens > maxExact || t.OutputTokens > maxExact-t.InputTokens || t.TotalTokens != t.InputTokens+t.OutputTokens {
		return usage.Usage{}, false
	}
	if t.InputTokens < 0 || t.CachedInputTokens < 0 || t.CacheWriteInputTokens < 0 || t.OutputTokens < 0 || t.ReasoningOutputTokens < 0 || t.TotalTokens < 0 || t.CachedInputTokens > t.InputTokens || t.CacheWriteInputTokens > t.InputTokens-t.CachedInputTokens || t.ReasoningOutputTokens > t.OutputTokens {
		return usage.Usage{}, false
	}
	// Native input already contains cached tokens; reasoning is an output detail.
	return usage.Usage{InputTokens: t.InputTokens - t.CachedInputTokens - t.CacheWriteInputTokens, CacheReadInputTokens: t.CachedInputTokens, CacheCreationInputTokens: t.CacheWriteInputTokens, OutputTokens: t.OutputTokens, ThinkingTokens: t.ReasoningOutputTokens}, true
}
func (o *codexObservation) disconnected() {
	o.mu.Lock()
	o.connected = false
	o.state = "unknown"
	o.status = "unknown"
	o.waiting = ""
	o.turnStatus = ""
	o.mu.Unlock()
	o.owner.notify()
}
func (o *codexObservation) finished() {
	o.mu.Lock()
	o.active = false
	o.connected = false
	o.mu.Unlock()
	o.owner.notify()
}
