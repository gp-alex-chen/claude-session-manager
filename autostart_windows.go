//go:build windows

package main

// 开机自启动（Windows）：写 HKCU\Software\Microsoft\Windows\CurrentVersion\Run，
// 当前用户级别，不需要管理员权限

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const runKeyName = "ClaudeSessionManager"

func setAutostart(enable bool) error {
	exe, err := exePath()
	if err != nil {
		return err
	}
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	if enable {
		v := exe
		if strings.ContainsAny(v, " ") {
			v = `"` + v + `"` // 路径含空格时注册表值需要引号
		}
		return k.SetStringValue(runKeyName, v)
	}
	if err := k.DeleteValue(runKeyName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

func isAutostart() bool {
	exe, err := exePath()
	if err != nil {
		return false
	}
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(runKeyName)
	if err != nil {
		return false
	}
	return strings.Trim(v, `"`) == exe
}
