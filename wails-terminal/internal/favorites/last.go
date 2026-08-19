// 上次会话记忆：独立的 last-session.json（与 favorites.json 同目录）。
// 不并入 favorites.json：该文件与 fyne 版共用，写入陌生键可能被旧版
// 覆盖掉；独立文件互不干扰。
package favorites

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// LastPath 上次会话文件路径：exe 同目录
func LastPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "last-session.json"
	}
	return filepath.Join(filepath.Dir(exe), "last-session.json")
}

// SaveLast 记录上次打开的会话 id。
func SaveLast(id string) {
	if id == "" {
		return
	}
	b, err := json.Marshal(map[string]string{"id": id})
	if err != nil {
		return
	}
	_ = os.WriteFile(LastPath(), b, 0o644)
}

// LastID 读取上次打开的会话 id（文件不存在/损坏时返回空串）。
func LastID() string {
	b, err := os.ReadFile(LastPath())
	if err != nil {
		return ""
	}
	var j struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(b, &j) != nil || j.ID == "" {
		return ""
	}
	return j.ID
}