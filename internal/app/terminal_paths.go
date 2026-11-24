package app

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unicode"

	"golang.org/x/sys/windows"
)

// OpenTerminalPath opens a path chosen from terminal output. Relative paths use
// the session directory supplied by the terminal controller.
func (a *App) OpenTerminalPath(dir, target, action string) error {
	return openTerminalPath(dir, target, action, openTerminalFile, openFolderFn)
}

func openTerminalPath(dir, target, action string, openFile, openFolder func(string) error) error {
	if action != "open" && action != "folder" {
		return errors.New("未知的路径操作")
	}
	path, err := resolveTerminalPath(dir, target)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("路径不可用 %q: %w", path, err)
	}
	if info.IsDir() {
		return openFolder(path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("无法打开此文件类型: %q", path)
	}
	if action == "folder" {
		return openFolder(filepath.Dir(path))
	}
	return openFile(path)
}

func resolveTerminalPath(dir, target string) (string, error) {
	if scheme, _, ok := strings.Cut(target, ":"); ok && strings.EqualFold(scheme, "file") {
		uri, err := url.Parse(target)
		if err != nil || uri.Opaque != "" || uri.User != nil || (uri.Host != "" && !strings.EqualFold(uri.Host, "localhost")) {
			return "", errors.New("无效的本地文件链接")
		}
		target = strings.TrimPrefix(uri.Path, "/")
		if !hasLocalDrive(target) {
			return "", errors.New("文件链接需要本地盘符")
		}
	}
	volume := filepath.VolumeName(target)
	text := target[len(volume):]
	if target == "" || strings.ContainsAny(text, `<>:"|?*`) || strings.IndexFunc(text, unicode.IsControl) >= 0 {
		return "", errors.New("无效的本地路径")
	}
	if !hasLocalDrive(target) {
		if strings.HasPrefix(target, `\`) || strings.HasPrefix(target, "/") || volume != "" {
			return "", errors.New("无效的本地路径")
		}
		if !hasLocalDrive(dir) {
			return "", errors.New("会话目录不可用，无法解析相对路径")
		}
		target = filepath.Join(dir, target)
	}
	path := filepath.Clean(target)
	name := strings.TrimLeft(path[2:], `\/`)
	if name != "" && !filepath.IsLocal(name) {
		return "", errors.New("无效的本地路径")
	}
	return path, nil
}

func hasLocalDrive(path string) bool {
	volume := filepath.VolumeName(path)
	return len(volume) == 2 && volume[1] == ':' &&
		strings.IndexByte("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz", volume[0]) >= 0 && filepath.IsAbs(path)
}

func openTerminalFile(path string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	dir, err := windows.UTF16PtrFromString(filepath.Dir(path))
	if err != nil {
		return err
	}

	// Shell associations may use COM handlers that require an STA.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err != nil &&
		err != syscall.Errno(windows.S_FALSE) {
		return fmt.Errorf("初始化 Windows Shell 失败: %w", err)
	}
	defer windows.CoUninitialize()
	return windows.ShellExecute(0, verb, file, nil, dir, windows.SW_SHOWNORMAL)
}
