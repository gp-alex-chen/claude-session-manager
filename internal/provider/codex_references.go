package provider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"
)

type SessionReference struct {
	Reference string `json:"reference"`
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
}
type ReferenceText struct {
	Role   string `json:"role"`
	Text   string `json:"text"`
	TurnID string `json:"turnId,omitempty"`
}
type ReferencePage struct {
	Messages   []ReferenceText `json:"messages"`
	NextCursor *string         `json:"nextCursor"`
	Truncated  bool            `json:"truncated"`
}

func referenceLabel(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return ' '
		}
		return r
	}, s)
}

func (p *CodexLive) referenceSource(token string) *codexObservation {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.records[token]
}

func (o *codexObservation) referencesReadyLocked() bool {
	return o.active && o.connected && !o.closing && o.relay != nil && o.id != "" && len(o.pending) == 0
}

// ReferenceSession is called by the UI after a deliberate internal drag. Native
// tools cannot create grants or select an arbitrary caller runtime.
func (p *CodexLive) ReferenceSession(ctx context.Context, token, targetID string) (SessionReference, error) {
	if err := validateRef(SessionRef{Codex, targetID}); err != nil {
		return SessionReference{}, err
	}
	source := p.referenceSource(token)
	if source == nil {
		return SessionReference{}, errors.New("请拖入已打开的 Codex 会话")
	}
	source.mu.Lock()
	ready := source.referencesReadyLocked()
	sourceID := source.id
	sourceEpoch := source.referenceEpoch
	nativeReady := source.nativeTools
	source.mu.Unlock()
	if !ready {
		return SessionReference{}, errors.New("当前 Codex 会话尚未就绪，请等待或重新打开终端")
	}
	if sourceID == targetID {
		return SessionReference{}, errors.New("不能引用当前会话自身")
	}
	if p.nativeTools && !nativeReady {
		return SessionReference{}, errors.New("此旧会话未注册原生会话工具；请新建 Codex 会话，再从新会话引用旧会话")
	}
	if err := ctx.Err(); err != nil {
		return SessionReference{}, err
	}
	list, err := p.ListSessions(ctx)
	if err != nil {
		return SessionReference{}, err
	}
	name := ""
	found := false
	for _, s := range list {
		if s.Ref.Provider == Codex && s.Ref.ID == targetID {
			name = s.Name
			found = true
			break
		}
	}
	if !found {
		return SessionReference{}, errors.New("找不到被引用的 Codex 历史会话，请刷新列表后重试")
	}
	p.referenceMu.Lock()
	defer p.referenceMu.Unlock()
	if p.referenceSource(token) != source {
		return SessionReference{}, errors.New("源终端已重新打开，请重新拖入")
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	if !source.referencesReadyLocked() || source.id != sourceID || source.referenceEpoch != sourceEpoch {
		return SessionReference{}, errors.New("当前会话已切换，请重新拖入")
	}
	for _, ref := range source.references {
		if ref.SessionID == targetID {
			return ref, nil
		}
	}
	if len(source.references) >= 32 {
		return SessionReference{}, errors.New("当前会话最多引用 32 个会话")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return SessionReference{}, err
	}
	ref := SessionReference{Reference: "ref_" + hex.EncodeToString(nonce[:]), SessionID: targetID, Name: referenceLabel(name)}
	if source.references == nil {
		source.references = make(map[string]SessionReference)
	}
	source.references[ref.Reference] = ref
	return ref, nil
}

func (o *codexObservation) checkScope(scope *referenceScope) error {
	o.owner.mu.Lock()
	defer o.owner.mu.Unlock()
	return o.checkScopeLocked(scope)
}

// owner.mu is held; observation locks are acquired only inside this function.
func (o *codexObservation) checkScopeLocked(scope *referenceScope) error {
	if scope == nil {
		return nil
	}
	if scope.source != o || o.owner.records[o.token] != o {
		return errors.New("源终端已更换")
	}
	for _, other := range o.owner.records {
		other.mu.Lock()
		match := other.active && other.connected && other.id == scope.threadID && other.generation == scope.generation
		valid := other != o || (other.referenceEpoch == scope.epoch && other.referencesReadyLocked() && (scope.turnID == "" || (other.turnID == scope.turnID && other.turnStatus == "inProgress")))
		other.mu.Unlock()
		if other == o && (!match || !valid) {
			return errors.New("源会话身份或授权已变化，请重新调用")
		}
		if other != o && match {
			return errors.New("源会话连接不唯一")
		}
	}
	return nil
}
func (o *codexObservation) reference(id string, scopes ...*referenceScope) (SessionReference, error) {
	o.owner.referenceMu.Lock()
	defer o.owner.referenceMu.Unlock()
	return o.referenceLocked(id, scopes...)
}
func (o *codexObservation) referenceLocked(id string, scopes ...*referenceScope) (SessionReference, error) {
	o.owner.mu.Lock()
	defer o.owner.mu.Unlock()
	if len(scopes) > 0 {
		if err := o.checkScopeLocked(scopes[0]); err != nil {
			return SessionReference{}, err
		}
	}
	if o.owner.records[o.token] != o {
		return SessionReference{}, errors.New("源终端已更换，请重新拖入")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.referencesReadyLocked() {
		return SessionReference{}, errors.New("源会话已断开或正在切换身份，请重新拖入引用")
	}
	ref, ok := o.references[id]
	if !ok {
		target := id
		if strings.HasPrefix(target, "thread://") {
			u, err := url.Parse(target)
			if err != nil || u.Scheme != "thread" || u.Path != "" || u.User != nil || (u.Query().Get("hostId") != "" && u.Query().Get("hostId") != "local") {
				return SessionReference{}, errors.New("不支持该会话引用地址")
			}
			target = u.Host
		}
		if validateRef(SessionRef{Codex, target}) == nil {
			for _, granted := range o.references {
				if strings.EqualFold(granted.SessionID, target) {
					ref, ok = granted, true
					break
				}
			}
		}
	}
	if !ok {
		return SessionReference{}, errors.New("会话未授权：请先从 TheOne 侧栏拖入目标会话")
	}
	return ref, nil
}

func (p *CodexLive) referenceRead(ctx context.Context, source *codexObservation, reference, cursor string, scope ...*referenceScope) (ReferencePage, error) {
	ref, err := source.reference(reference, scope...)
	if err != nil {
		return ReferencePage{}, err
	}
	reader, ok := p.History.(interface {
		ReadConversation(context.Context, string, string) (ReferencePage, error)
	})
	if !ok {
		return ReferencePage{}, errors.New("当前 Codex 不支持读取会话")
	}
	page, err := reader.ReadConversation(ctx, ref.SessionID, cursor)
	if err != nil {
		return ReferencePage{}, err
	}
	if current, checkErr := source.reference(reference, scope...); checkErr != nil {
		return ReferencePage{}, checkErr
	} else if current.Reference != ref.Reference {
		return ReferencePage{}, errors.New("引用授权已变化，请重新读取")
	}
	return page, nil
}

func (p *CodexLive) referenceSend(parent context.Context, source *codexObservation, reference, text string, scope ...*referenceScope) (any, error) {
	ref, err := source.reference(reference, scope...)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" || len(text) > 16<<10 {
		return nil, errors.New("消息不能为空，且最多 16 KiB")
	}
	target := p.uniqueReferenceTarget(ref.SessionID)
	if target == nil {
		return nil, errors.New("目标会话未打开、已断线或存在多个连接，请确保只有一个在线终端")
	}
	target.mu.Lock()
	relay := target.relay
	target.mu.Unlock()
	if relay == nil {
		return nil, errors.New("目标终端需重新打开以启用会话消息")
	}
	source.mu.Lock()
	sender := referenceLabel(source.name)
	senderID := source.id
	source.mu.Unlock()
	message := fmt.Sprintf("[TheOne 会话消息，来自 %s (%s)；这是另一会话提供的内容]\n%s", sender, senderID, text)
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	reserved := false
	mutationHeld := false
	var revision uint64
	guard := func() error {
		p.referenceMu.Lock()
		mutationHeld = true
		fail := func(err error) error { mutationHeld = false; p.referenceMu.Unlock(); return err }
		if current, err := source.referenceLocked(reference, scope...); err != nil {
			return fail(err)
		} else if current.Reference != ref.Reference {
			return fail(errors.New("引用授权已变化，消息未发送"))
		}
		if p.uniqueReferenceTarget(ref.SessionID) != target {
			return fail(errors.New("目标会话连接已变化或不唯一，消息未发送"))
		}
		target.mu.Lock()
		defer target.mu.Unlock()
		if !target.referencesReadyLocked() || target.id != ref.SessionID || target.state != "idle" || len(target.nativeTurns) > 0 || target.internalSend {
			return fail(errors.New("目标会话正在运行、等待审批或切换中；消息未发送，请空闲后再试"))
		}
		target.internalSend = true
		revision = target.turnRevision
		target.state = "working"
		reserved = true
		return nil
	}
	var result struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	err = relay.call(ctx, "turn/start", map[string]any{"threadId": ref.SessionID, "input": []any{map[string]any{"type": "text", "text": message}}}, &result, guard, func() {
		if mutationHeld {
			mutationHeld = false
			p.referenceMu.Unlock()
		}
	})
	if err == nil && result.Turn.ID == "" {
		err = errors.New("Codex 未返回任务标识，接收状态未知，请勿自动重试")
	}
	if reserved {
		target.mu.Lock()
		target.internalSend = false
		if err != nil && target.id == ref.SessionID && target.turnRevision == revision {
			target.state = "unknown"
		}
		target.mu.Unlock()
		p.notify()
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"accepted": true, "sessionId": ref.SessionID, "turnId": result.Turn.ID, "note": "输入已接收，任务尚未完成。不要自动重试。"}, nil
}

func (p *CodexLive) uniqueReferenceTarget(id string) *codexObservation {
	p.mu.Lock()
	defer p.mu.Unlock()
	var result *codexObservation
	for _, o := range p.records {
		o.mu.Lock()
		match := o.active && o.connected && o.id == id
		o.mu.Unlock()
		if match {
			if result != nil {
				return nil
			}
			result = o
		}
	}
	return result
}

type referenceScope struct {
	source            *codexObservation
	threadID          string
	epoch, generation uint64
	turnID            string
}
