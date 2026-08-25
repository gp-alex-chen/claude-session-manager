package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func normalizeAppProjectDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", errors.New("项目目录不能为空")
	}
	normalized, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return "", fmt.Errorf("规范化项目目录失败: %w", err)
	}
	return normalized, nil
}

func (a *App) ListProjects() []string {
	projects, err := a.store.LoadProjects()
	if err != nil {
		a.DebugLog("读取 projects.json 失败: " + err.Error())
		return []string{}
	}
	if projects == nil {
		return []string{}
	}
	return projects
}

func (a *App) ChooseProjectDir() (string, error) {
	chooseDir := a.chooseDirFn
	if chooseDir == nil {
		chooseDir = runtime.OpenDirectoryDialog
	}
	return chooseDir(a.runtimeContext(), runtime.OpenDialogOptions{Title: "选择项目目录"})
}

func (a *App) AddProject(dir string) error {
	normalized, err := normalizeAppProjectDir(dir)
	if err != nil {
		return err
	}
	info, err := os.Stat(normalized)
	if err != nil {
		return fmt.Errorf("项目目录不可用 %q: %w", normalized, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("项目路径不是目录: %q", normalized)
	}
	for _, existing := range a.ListProjects() {
		if strings.EqualFold(existing, normalized) {
			return nil
		}
	}
	return a.store.AddProject(normalized)
}

func (a *App) DeleteProject(dir string) error {
	normalized, err := normalizeAppProjectDir(dir)
	if err != nil {
		return err
	}
	return a.store.DeleteProject(normalized)
}
