// Package favorites 本地状态文件 favorites.json（与 fyne-sidebar 版同格式，
// 可共用同一文件）：
//   {"ids":[收藏...], "aliases":{会话ID:别名}, "hidden":[已删除(软隐藏)的ID...]}
// 重命名 = 本地别名（只改本软件显示名，不碰 claude 数据文件）；
// 删除 = 软隐藏（只记录 ID 到 hidden，界面不再显示，不物理删除会话文件）。
package favorites

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// FavState 状态文件内容。
type FavState struct {
	Ids     []string          `json:"ids"`
	Aliases map[string]string `json:"aliases"`
	Hidden  []string          `json:"hidden"`
}

// FavPath 状态文件路径：exe 同目录（与 fyne-sidebar 版约定一致）
func FavPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "favorites.json"
	}
	return filepath.Join(filepath.Dir(exe), "favorites.json")
}

// Load 读取并解析 favorites.json（文件不存在/损坏时返回空状态）
func Load() *FavState {
	st := &FavState{Aliases: map[string]string{}}
	b, err := os.ReadFile(FavPath())
	if err != nil {
		return st
	}
	_ = json.Unmarshal(b, st)
	if st.Aliases == nil {
		st.Aliases = map[string]string{}
	}
	return st
}

func Save(st *FavState) {
	if st.Aliases == nil {
		st.Aliases = map[string]string{}
	}
	b, err := json.Marshal(map[string]any{
		"ids":     st.Ids,
		"aliases": st.Aliases,
		"hidden":  st.Hidden,
	})
	if err != nil {
		return
	}
	_ = os.WriteFile(FavPath(), b, 0o644)
}

// HiddenSet 返回 hidden 集合（查用）。
func (st *FavState) HiddenSet() map[string]bool {
	m := make(map[string]bool, len(st.Hidden))
	for _, h := range st.Hidden {
		m[h] = true
	}
	return m
}

// RemoveHidden 从 hidden 移除一个 id。
func (st *FavState) RemoveHidden(id string) {
	out := st.Hidden[:0]
	for _, h := range st.Hidden {
		if h != id {
			out = append(out, h)
		}
	}
	st.Hidden = out
}