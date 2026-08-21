// 应用设置：独立的 settings.json（与 favorites.json 同目录）。
// 目前只存"底层 Shell"选择（cmd / pwsh）。独立文件，不污染 fyne 共用的
// favorites.json。
package favorites

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ShellPath 设置文件路径：exe 同目录
func ShellPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "settings.json"
	}
	return filepath.Join(filepath.Dir(exe), "settings.json")
}

// SaveShell 记录底层 Shell（仅接受 cmd / pwsh；其余一律回退 cmd）。
func SaveShell(name string) {
	if name != "cmd" && name != "pwsh" {
		name = "cmd"
	}
	b, err := json.Marshal(map[string]string{"shell": name})
	if err != nil {
		return
	}
	_ = os.WriteFile(ShellPath(), b, 0o644)
}

// Shell 返回底层 Shell；未设置/损坏/非法值一律默认 cmd。
func Shell() string {
	b, err := os.ReadFile(ShellPath())
	if err != nil {
		return "cmd"
	}
	var j struct {
		Shell string `json:"shell"`
	}
	if json.Unmarshal(b, &j) != nil || j.Shell == "" {
		return "cmd"
	}
	if j.Shell != "cmd" && j.Shell != "pwsh" {
		return "cmd"
	}
	return j.Shell
}