package main

// 公共：获取本程序 exe 的绝对路径（自启动项使用）

import (
	"os"
	"path/filepath"
)

func exePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(exe)
}
