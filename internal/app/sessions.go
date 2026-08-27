package app

// App 的会话别名、归档与 Shell 设置绑定。

import (
	"errors"
	"sort"
	"strings"

	"github.com/gp-alex-chen/claude-session-manager/internal/session"
)

// shellAvailable 底层 Shell 是否可用：cmd.exe 系统自带恒可用；
// pwsh 需要在 PATH 中能找到（PowerShell 7）。
func (a *App) shellAvailable(name string) bool {
	if name != "pwsh" {
		return true
	}
	_, err := a.lookPath("pwsh")
	return err == nil
}

// RenameSession 设置/清除会话别名（空串 = 清除，恢复原名）。
// 注意：只改本软件的显示名；真正重命名请用 claude 会话内的 /rename。
func (a *App) RenameSession(id, name string) error {
	name = strings.TrimSpace(name)
	return a.store.SetAlias(id, name)
}

// DeleteSession 软删除：把会话 ID 记入 hidden，列表不再显示。
// 不物理删除 ~/.claude 下的会话文件；可在"归档"面板恢复。
func (a *App) DeleteSession(id string) error {
	return a.store.SetHidden(id, true)
}

// UnhideSession 从 hidden 移除，恢复显示。
func (a *App) UnhideSession(id string) error {
	return a.store.SetHidden(id, false)
}

// GetOpenSessions 返回上次关闭时仍打开着（有存活终端）的会话 ID 集合，
// 启动时前端据此把所有这些会话全部恢复。集合由后端在会话打开/关闭时
// 自动维护（persistOpenSessions），前端只要启动时读一次。
func (a *App) GetOpenSessions() []string {
	ids, err := a.store.LoadOpen()
	if err != nil {
		a.DebugLog("读取 open-sessions.json 失败: " + err.Error())
	}
	return ids
}

// AdoptSession preserves the runtime token used by the terminal while making
// the real Claude session ID available for open-session persistence.
func (a *App) AdoptSession(runtimeToken, sessionID string) error {
	runtimeToken = strings.TrimSpace(runtimeToken)
	sessionID = strings.TrimSpace(sessionID)
	if runtimeToken == "" || sessionID == "" {
		return errors.New("runtime token and session ID are required")
	}
	if err := session.ValidateID(sessionID); err != nil {
		return err
	}
	if a.terms == nil {
		return errors.New("terminal manager is unavailable")
	}
	pending, err := a.registerPendingAdoption(runtimeToken, sessionID)
	if err != nil {
		return err
	}
	defer a.unregisterPendingAdoption(runtimeToken, sessionID)
	if err := a.terms.Adopt(runtimeToken, sessionID); err != nil {
		return err
	}
	if pending.cancelled.Load() {
		// A user close may have raced with Adopt after Manager associated the
		// real ID. Remove that association before returning success to Wails.
		a.terms.Kill(runtimeToken)
		return errors.New("adoption was cancelled")
	}
	return nil
}

func (a *App) registerPendingAdoption(runtimeToken, sessionID string) (*pendingAdoption, error) {
	a.adoptionMu.Lock()
	defer a.adoptionMu.Unlock()
	if a.pendingAdoptions == nil {
		a.pendingAdoptions = make(map[string]*pendingAdoption)
	}
	if a.cancelledAdoptions == nil {
		a.cancelledAdoptions = make(map[string]struct{})
	}
	if _, cancelled := a.cancelledAdoptions[runtimeToken]; cancelled {
		return nil, errors.New("adoption was cancelled")
	}
	if pending, ok := a.pendingAdoptions[runtimeToken]; ok && pending.id != sessionID {
		return nil, errors.New("runtime token already has a different pending adoption")
	}
	pending := a.pendingAdoptions[runtimeToken]
	if pending == nil {
		pending = &pendingAdoption{id: sessionID}
	}
	pending.id = sessionID
	pending.count++
	a.pendingAdoptions[runtimeToken] = pending
	return pending, nil
}

func (a *App) unregisterPendingAdoption(runtimeToken, sessionID string) {
	a.adoptionMu.Lock()
	defer a.adoptionMu.Unlock()
	pending, ok := a.pendingAdoptions[runtimeToken]
	if !ok || pending.id != sessionID {
		return
	}
	if pending.count <= 1 {
		delete(a.pendingAdoptions, runtimeToken)
		return
	}
	pending.count--
	a.pendingAdoptions[runtimeToken] = pending
}

func (a *App) cancelPendingAdoption(runtimeToken string) {
	runtimeToken = strings.TrimSpace(runtimeToken)
	if runtimeToken == "" {
		return
	}
	if !strings.HasPrefix(runtimeToken, "new-") {
		return
	}
	a.adoptionMu.Lock()
	defer a.adoptionMu.Unlock()
	if a.cancelledAdoptions == nil {
		a.cancelledAdoptions = make(map[string]struct{})
	}
	a.cancelledAdoptions[runtimeToken] = struct{}{}
	if pending := a.pendingAdoptions[runtimeToken]; pending != nil {
		pending.cancelled.Store(true)
	}
}

func (a *App) clearAdoptionCancellation(runtimeToken string) {
	a.adoptionMu.Lock()
	delete(a.cancelledAdoptions, runtimeToken)
	a.adoptionMu.Unlock()
}

func (a *App) pendingAdoptionIDsLocked() []string {
	ids := make([]string, 0, len(a.pendingAdoptions))
	seen := make(map[string]struct{}, len(a.pendingAdoptions))
	for _, pending := range a.pendingAdoptions {
		if pending == nil || pending.cancelled.Load() || pending.count <= 0 || pending.id == "" {
			continue
		}
		if _, ok := seen[pending.id]; ok {
			continue
		}
		seen[pending.id] = struct{}{}
		ids = append(ids, pending.id)
	}
	sort.Strings(ids)
	return ids
}

func (a *App) cancelledAdoptionTokensLocked() []string {
	seen := make(map[string]struct{}, len(a.cancelledAdoptions)+len(a.pendingAdoptions))
	for token := range a.cancelledAdoptions {
		seen[token] = struct{}{}
	}
	for token, pending := range a.pendingAdoptions {
		if pending != nil && pending.cancelled.Load() {
			seen[token] = struct{}{}
		}
	}
	tokens := make([]string, 0, len(seen))
	for token := range seen {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	return tokens
}

func validateSessionID(id string) error {
	return session.ValidateID(id)
}

// GetShell 当前底层 Shell（cmd / pwsh，默认 cmd）。
func (a *App) GetShell() string {
	shell, err := a.store.Shell()
	if err != nil {
		a.DebugLog("读取 settings.json 失败: " + err.Error())
	}
	return shell
}

// ShellInstalled 查询某个 Shell 是否可用（前端选型时预检用）。
func (a *App) ShellInstalled(name string) bool {
	return a.shellAvailable(name)
}

// SetShell 指定底层 Shell（cmd / pwsh），写盘记忆。
// 选择 pwsh 时会先校验系统是否装了 pwsh：未安装则拒绝并返回错误，
// 避免"选完打不开"；只影响之后新启动/恢复的会话。
func (a *App) SetShell(name string) error {
	if name == "pwsh" && !a.shellAvailable("pwsh") {
		return errors.New("未检测到 pwsh（PowerShell 7）：请先安装并确保 pwsh 在 PATH 中，或保持 cmd")
	}
	return a.store.SetShell(name)
}
