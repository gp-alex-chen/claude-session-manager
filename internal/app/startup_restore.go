package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/gp-alex-chen/claude-session-manager/internal/provider"
)

type SessionRestoreResult struct {
	Token   string                 `json:"token"`
	History ProviderSessionsResult `json:"history"`
}
type sessionRestoreBatch struct {
	token      string
	provider   provider.ID
	generation uint64
	ctx        context.Context
	cancel     context.CancelFunc
	history    provider.StartupHistory
	sessions   map[string]SessionInfo
	consumed   map[string]bool
	launches   map[string]context.CancelFunc
	expires    time.Time
}

// BeginSessionRestore owns both the displayed history and the launch proof.
// The browser receives an opaque lease, never authority to supply a catalog.
func (a *App) BeginSessionRestore(id string) (SessionRestoreResult, error) {
	if provider.ID(id) != provider.Antigravity {
		return SessionRestoreResult{}, errors.New("此来源不支持批量启动恢复")
	}
	p, err := a.providers.Lookup(provider.ID(id))
	if err != nil {
		return SessionRestoreResult{}, err
	}
	source, ok := p.(provider.StartupHistoryProvider)
	if !ok {
		return SessionRestoreResult{}, errors.New("此来源不支持批量启动恢复")
	}
	a.lifecycleMu.Lock()
	saved, err := a.captureStartupRestoreReferencesLocked()
	generation, parent, stopping := a.lifecycleID, a.ctx, a.stopping
	a.lifecycleMu.Unlock()
	if err != nil {
		return SessionRestoreResult{}, err
	}
	allowed := map[string]bool{}
	for _, key := range saved {
		ref, err := provider.DecodeKey(key)
		if err == nil && ref.Provider == provider.ID(id) && p.ValidateSessionID(ref.ID) == nil {
			allowed[key] = false
		}
	}
	if stopping || (generation != 0 && parent == nil) {
		return SessionRestoreResult{}, errors.New("应用已关闭")
	}
	if parent == nil {
		parent = context.Background()
	}
	nonce := make([]byte, 24)
	if _, err = rand.Read(nonce); err != nil {
		return SessionRestoreResult{}, err
	}
	ctx, cancel := context.WithCancel(parent)
	batch := &sessionRestoreBatch{token: hex.EncodeToString(nonce), provider: provider.ID(id), generation: generation, ctx: ctx, cancel: cancel, consumed: allowed, sessions: map[string]SessionInfo{}}
	a.restoreMu.Lock()
	if a.restoreBatches == nil {
		a.restoreBatches = map[string]*sessionRestoreBatch{}
	}
	for token, old := range a.restoreBatches {
		if old.provider == batch.provider {
			old.cancel()
			delete(a.restoreBatches, token)
		}
	}
	a.restoreBatches[batch.token] = batch
	a.restoreMu.Unlock()
	success := false
	defer func() {
		if !success {
			a.EndSessionRestore(batch.token)
		}
	}()
	rows, history, err := source.ReadStartupHistory(ctx)
	if err != nil {
		return SessionRestoreResult{}, err
	}
	if err = ctx.Err(); err != nil {
		return SessionRestoreResult{}, err
	}
	valid := []provider.Session{}
	for _, row := range rows {
		if row.Ref.Provider == batch.provider && p.ValidateSessionID(row.Ref.ID) == nil {
			valid = append(valid, row)
		}
	}
	result := a.formatProviderSessions(valid, []provider.ID{batch.provider}, nil)
	a.restoreMu.Lock()
	defer a.restoreMu.Unlock()
	a.lifecycleMu.Lock()
	current := a.lifecycleID == generation && !a.stopping && (generation == 0 || a.ctx != nil)
	closed := map[string]bool{}
	for key, value := range a.startupClosedIDs {
		closed[key] = value
	}
	a.lifecycleMu.Unlock()
	if !current || a.restoreBatches[batch.token] != batch || ctx.Err() != nil {
		return SessionRestoreResult{}, errors.New("启动恢复已取消")
	}
	if history == nil {
		return SessionRestoreResult{}, errors.New("启动历史未提供完整快照")
	}
	batch.history = history
	batch.expires = time.Now().Add(2 * time.Minute)
	for _, row := range result.Sessions {
		if _, ok := allowed[row.ID]; ok && !closed[row.ID] {
			batch.sessions[row.ID] = row
		}
	}
	success = true
	return SessionRestoreResult{Token: batch.token, History: result}, nil
}

func (a *App) RestoreSession(token, id string) (string, error) {
	a.restoreMu.Lock()
	batch := a.restoreBatches[token]
	if batch == nil || batch.ctx.Err() != nil || batch.history == nil || !time.Now().Before(batch.expires) {
		a.restoreMu.Unlock()
		return "", errors.New("启动恢复凭据无效或已过期，请手动打开")
	}
	row, allowed := batch.sessions[id]
	used, known := batch.consumed[id]
	a.lifecycleMu.Lock()
	current := a.lifecycleID == batch.generation && !a.stopping && (batch.generation == 0 || a.ctx != nil) && !a.startupClosedIDs[id]
	a.lifecycleMu.Unlock()
	if !current || !allowed || !known || used {
		a.restoreMu.Unlock()
		return "", errors.New("此会话的启动恢复已失效或已取消")
	}
	ref, err := provider.DecodeKey(id)
	if err != nil || ref.Provider != batch.provider {
		a.restoreMu.Unlock()
		return "", errors.New("启动恢复来源不一致")
	}
	batch.consumed[id] = true
	ctx, cancel := context.WithDeadline(batch.ctx, batch.expires)
	if batch.launches == nil {
		batch.launches = map[string]context.CancelFunc{}
	}
	batch.launches[id] = cancel
	history := batch.history
	a.restoreMu.Unlock()
	defer cancel()
	if err := a.startAntigravityPTYWithHistory(id, &ref, row.Dir, history, ctx); err != nil {
		return "", err
	}
	return id, nil
}

func (a *App) EndSessionRestore(token string) {
	a.restoreMu.Lock()
	defer a.restoreMu.Unlock()
	if batch := a.restoreBatches[token]; batch != nil {
		batch.cancel()
		delete(a.restoreBatches, token)
	}
}
func (a *App) invalidateSessionRestores() { a.invalidateSessionRestoreGenerations(0, true, true) }
func (a *App) invalidateSessionRestoreGenerations(generation uint64, older, all bool) {
	a.restoreMu.Lock()
	defer a.restoreMu.Unlock()
	for token, batch := range a.restoreBatches {
		if all || (older && batch.generation < generation) || (!older && batch.generation == generation) {
			batch.cancel()
			delete(a.restoreBatches, token)
		}
	}
}
func (a *App) revokeSessionRestore(id string) {
	a.lifecycleMu.Lock()
	_, _ = a.captureStartupRestoreReferencesLocked()
	if a.startupClosedIDs == nil {
		a.startupClosedIDs = map[string]bool{}
	}
	a.startupClosedIDs[id] = true
	a.lifecycleMu.Unlock()
	a.restoreMu.Lock()
	defer a.restoreMu.Unlock()
	for _, batch := range a.restoreBatches {
		if _, ok := batch.consumed[id]; ok {
			batch.consumed[id] = true
			if cancel := batch.launches[id]; cancel != nil {
				cancel()
			}
		}
	}
}

// lifecycleMu guards the original startup set. Live Manager persistence may
// change the file while slower providers are still obtaining their history.
func (a *App) captureStartupRestoreReferencesLocked() ([]string, error) {
	// Shutdown advances lifecycleID before its final atomic close. Late reads
	// and revocations must keep the stopping owner's snapshot and flags.
	if a.stopping && !a.startupSavedReady {
		return nil, nil
	}
	if !a.startupSavedReady || (!a.stopping && a.startupSavedGeneration != a.lifecycleID) {
		a.startupSavedIDs, a.startupSavedError = a.store.LoadOpen()
		a.startupSavedReady = true
		a.startupSavedGeneration = a.lifecycleID
		a.startupClosedIDs = map[string]bool{}
		a.startupStartedIDs = map[string]bool{}
	}
	ids := make([]string, 0, len(a.startupSavedIDs))
	for _, id := range a.startupSavedIDs {
		if !a.startupClosedIDs[id] {
			ids = append(ids, id)
		}
	}
	return ids, a.startupSavedError
}

func (a *App) noteStartupSessionStarted(id string) {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if !a.startupSavedReady || a.startupSavedGeneration != a.lifecycleID {
		return
	}
	for _, saved := range a.startupSavedIDs {
		if saved == id {
			a.startupStartedIDs[id] = true
			return
		}
	}
}

// Called at the final atomic close with agentLaunchMu held. Carry preserves
// only the previous startup intent, never a launched but unconfirmed identity.
func (a *App) pendingStartupRestoreReferences(generation uint64) []string {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if !a.startupSavedReady || a.startupSavedGeneration != generation {
		return nil
	}
	ids := []string{}
	for _, key := range a.startupSavedIDs {
		if a.startupClosedIDs[key] || a.startupStartedIDs[key] {
			continue
		}
		ref, err := provider.DecodeKey(key)
		if err != nil {
			continue
		}
		p, err := a.providers.Lookup(ref.Provider)
		if err == nil && p.ValidateSessionID(ref.ID) == nil {
			ids = append(ids, key)
		}
	}
	return ids
}
