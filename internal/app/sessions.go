package app

// App 的会话别名、归档与 Shell 设置绑定。

import (
	"errors"
	"strings"
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
	st := a.store.Load()
	name = strings.TrimSpace(name)
	if name == "" {
		delete(st.Aliases, id)
	} else {
		st.Aliases[id] = name
	}
	return a.store.Save(st)
}

// DeleteSession 软删除：把会话 ID 记入 hidden，列表不再显示。
// 不物理删除 ~/.claude 下的会话文件；可在"归档"面板恢复。
func (a *App) DeleteSession(id string) error {
	st := a.store.Load()
	if !st.HiddenSet()[id] {
		st.Hidden = append(st.Hidden, id)
		return a.store.Save(st)
	}
	return nil
}

// UnhideSession 从 hidden 移除，恢复显示。
func (a *App) UnhideSession(id string) error {
	st := a.store.Load()
	if st.HiddenSet()[id] {
		st.RemoveHidden(id)
		return a.store.Save(st)
	}
	return nil
}

// GetOpenSessions 返回上次关闭时仍打开着（有存活终端）的会话 ID 集合，
// 启动时前端据此把所有这些会话全部恢复。集合由后端在会话打开/关闭时
// 自动维护（persistOpenSessions），前端只要启动时读一次。
func (a *App) GetOpenSessions() []string {
	return a.store.LoadOpen()
}

// GetShell 当前底层 Shell（cmd / pwsh，默认 cmd）。
func (a *App) GetShell() string {
	return a.store.Shell()
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
	return a.store.SaveShell(name)
}
