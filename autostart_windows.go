//go:build windows

package main

// 开机自启动（Windows）：写“启动”文件夹里的 .vbs。
// wscript 静默拉起本程序（窗口隐藏），无控制台窗口、无注册表、无需管理员权限。
// 用户可直接在 %APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup 看到/删除。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const autostartFileName = "ClaudeSessionManager.vbs"

func autostartFilePath() (string, error) {
	dir, err := os.UserConfigDir() // Windows 下为 %APPDATA%
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Microsoft", "Windows", "Start Menu", "Programs", "Startup", autostartFileName), nil
}

func setAutostart(enable bool) error {
	exe, err := exePath()
	if err != nil {
		return err
	}
	p, err := autostartFilePath()
	if err != nil {
		return err
	}
	if !enable {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	// WScript.Shell.Run 第二个参数 0 = 隐藏窗口启动；路径放进双引号以兼容空格
	esc := strings.ReplaceAll(exe, `"`, `""`)
	content := fmt.Sprintf("CreateObject(\"WScript.Shell\").Run \"\"\"%s\"\"\", 0, False\n", esc)
	return os.WriteFile(p, []byte(content), 0o644)
}

func isAutostart() bool {
	exe, err := exePath()
	if err != nil {
		return false
	}
	p, err := autostartFilePath()
	if err != nil {
		return false
	}
	b, err := os.ReadFile(p)
	return err == nil && strings.Contains(string(b), exe)
}
