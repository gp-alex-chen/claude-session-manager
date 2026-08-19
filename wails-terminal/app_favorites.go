package main

// App 的 favorites 相关绑定方法（数据层在 internal/favorites）。

import (
	"strings"

	"claude-terminal/internal/favorites"
)

// RenameSession 设置/清除会话别名（空串 = 清除，恢复原名）。
// 注意：只改本软件的显示名；真正重命名请用 claude 会话内的 /rename。
func (a *App) RenameSession(id, name string) error {
	st := favorites.Load()
	name = strings.TrimSpace(name)
	if name == "" {
		delete(st.Aliases, id)
	} else {
		st.Aliases[id] = name
	}
	favorites.Save(st)
	return nil
}

// DeleteSession 软删除：把会话 ID 记入 hidden，列表不再显示。
// 不物理删除 ~/.claude 下的会话文件；可在"归档"面板恢复。
func (a *App) DeleteSession(id string) error {
	st := favorites.Load()
	if !st.HiddenSet()[id] {
		st.Hidden = append(st.Hidden, id)
		favorites.Save(st)
	}
	return nil
}

// UnhideSession 从 hidden 移除，恢复显示。
func (a *App) UnhideSession(id string) error {
	st := favorites.Load()
	if st.HiddenSet()[id] {
		st.RemoveHidden(id)
		favorites.Save(st)
	}
	return nil
}

// GetLastSession 返回上次打开/使用过的会话 id（启动时前端自动恢复）。
func (a *App) GetLastSession() string {
	return favorites.LastID()
}

// SetLastSession 记录"当前正在使用的会话"（前端切换激活会话时调用）。
// new- 前缀的 token 是临时新建会话，重启后无法恢复，不记录。
func (a *App) SetLastSession(id string) {
	if strings.HasPrefix(id, "new-") {
		return
	}
	favorites.SaveLast(id)
}
