//go:build linux

package main

// 开机自启动（Linux）：写 ~/.config/autostart/claude-session-manager.desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const autostartFileName = "claude-session-manager.desktop"

func autostartFilePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "autostart", autostartFileName), nil
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
	content := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=Claude 会话管理\nExec=%s\nX-GNOME-Autostart-enabled=true\n", exe)
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
