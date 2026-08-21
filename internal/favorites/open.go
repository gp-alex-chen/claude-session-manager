// 打开的会话集合：独立的 open-sessions.json（与 favorites.json 同目录）。
// 语义：上次应用关闭时仍打开着（有存活终端）的会话 ID 集合；
// 启动时前端据此把所有这些会话全部恢复。独立文件，不污染 fyne 共用的
// favorites.json。
package favorites

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// OpenPath 打开会话集合文件路径：exe 同目录
func OpenPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "open-sessions.json"
	}
	return filepath.Join(filepath.Dir(exe), "open-sessions.json")
}

// SaveOpen 持久化打开会话 ID 集合（内部排序，保证文件内容稳定）。
func SaveOpen(ids []string) {
	sort.Strings(ids)
	b, err := json.Marshal(map[string][]string{"ids": ids})
	if err != nil {
		return
	}
	_ = os.WriteFile(OpenPath(), b, 0o644)
}

// LoadOpen 读取上次打开着的会话 ID 集合（文件不存在/损坏时返回 nil）。
func LoadOpen() []string {
	b, err := os.ReadFile(OpenPath())
	if err != nil {
		return nil
	}
	var j struct {
		Ids []string `json:"ids"`
	}
	if json.Unmarshal(b, &j) != nil {
		return nil
	}
	return j.Ids
}