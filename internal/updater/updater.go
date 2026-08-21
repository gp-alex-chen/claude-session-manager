// Package updater 实现"一键更新到最新版"。
//
// 更新源：本项目 GitHub Releases —— 按 v*-wails tag 发布、资产名为
// claude-terminal.exe 的 Windows 可执行程序。
//
// 链路：GitHub API 查最新正式版 -> 从 Release 下载 exe -> 替换自身
// -> 自动重启进新版（Windows 上运行中的 exe 允许改名不允许删除，
// 因此当前程序先改名 .old，新文件落地到原名，再启动新版并退出）。
package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const (
	// apiBase 查询某仓库全部 release（每页 100 个足够本项目使用；
	// 名额由 tag 前缀/后缀筛选，这里不再做分页）。
	apiBase = "https://api.github.com/repos/%s/%s/releases?per_page=100"
	// downloadFmt 资产直链；GitHub 会 302 到 objects.githubusercontent.com 加速节点。
	downloadFmt = "https://github.com/%s/%s/releases/download/%s/%s"
	// maxDownload 更新包大小上限（防异常/恶意大文件吃满磁盘）。
	maxDownload = 512 << 20 // 512 MiB

	// userAgent HTTP User-Agent（GitHub API 要求带 UA）。
	userAgent = "claude-terminal-updater"
)

// Release GitHub Release 的最小字段（仅用于筛选最新 wails 版）。
type Release struct {
	Tag string `json:"tag_name"`
	Pre bool   `json:"prerelease"`
}

// Info 检查结果，直接序列化给前端展示/决策。
type Info struct {
	// Current 本机版本（由 internal/app.Version 经 -ldflags 注入；未注入为 "dev"）
	Current string `json:"current"`
	// Latest 最新可用版本号（无则为空串）
	Latest string `json:"latest"`
	// LatestTag 最新可用 tag（如 v0.2-wails）
	LatestTag string `json:"latestTag"`
	// URL 资产直链
	URL string `json:"url"`
	// HasUpdate 是否存在比本机更新的正式版
	HasUpdate bool `json:"hasUpdate"`
}

// ProgressFunc 下载进度回调：percent 0-100，downloaded/total 字节数。
type ProgressFunc func(percent int, downloaded, total int64)

// Updater 面向具体仓库/资产的更新器。
type Updater struct {
	Owner, Repo, Asset string // 仓库与资产名
	Current            string // 本机版本（internal/app.Version）
	IncludePrerelease  bool   // 是否把 GitHub 预发布（v*-pre / v*-rc）也当作可更新目标
	Client             *http.Client
}

// New 构造更新器；owner/repo/asset 例如
// ("gp-alex-chen", "claude-session-manager", "claude-terminal.exe", Version)。
func New(owner, repo, asset, current string) *Updater {
	return &Updater{
		Owner:   owner,
		Repo:    repo,
		Asset:   asset,
		Current: current,
		Client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Check 查询最新可用的 wails 正式版并判断是否有更新。
// 任何一步失败都返回错误（由上层转成用户可读文案）。
func (u *Updater) Check(ctx context.Context) (*Info, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf(apiBase, u.Owner, u.Repo), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := u.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("检查更新失败（无法访问 GitHub）: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("检查更新失败: GitHub API 返回 HTTP %s", resp.Status)
	}

	var list []Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&list); err != nil {
		return nil, fmt.Errorf("检查更新失败（解析响应异常）: %w", err)
	}

	info := &Info{Current: u.Current}
	rel, ok := pickLatest(list, u.IncludePrerelease)
	if !ok {
		// 没有任何 wails 正式版发布：视为无更新
		return info, nil
	}
	info.Latest = rel.Tag
	info.LatestTag = rel.Tag
	info.URL = u.downloadURL(rel.Tag)
	info.HasUpdate = compareToCurrent(u.Current, rel.Tag)
	return info, nil
}

// downloadURL 拼接某 tag 下资产的直链。
func (u *Updater) downloadURL(tag string) string {
	return fmt.Sprintf(downloadFmt, u.Owner, u.Repo, tag, u.Asset)
}

// DownloadTo 从信息指定的 Release 直链把资产流式下载到 dest，
// 期间通过 progress 回调回报百分比（每变化 1% 回调一次）。
// 下载完成会做基本校验（非空 + PE 头），失败时清理 dest。
func (u *Updater) DownloadTo(ctx context.Context, info *Info, dest string, progress ProgressFunc) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := u.Client.Do(req)
	if err != nil {
		return fmt.Errorf("下载更新失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载更新失败: HTTP %s", resp.Status)
	}
	if total := resp.ContentLength; total > maxDownload {
		return fmt.Errorf("更新包过大（%d 字节），已中止", total)
	}

	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("无法创建临时文件: %w", err)
	}
	total := resp.ContentLength
	written := int64(0)
	lastPct := -1
	buf := make([]byte, 256<<10)
	// 循环读流写盘：不用 io.Copy 是为了拿到字节数做进度
	readErr := error(nil)
	for {
		var n int
		n, readErr = resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				_ = f.Close()
				_ = os.Remove(dest)
				return fmt.Errorf("写入临时文件失败: %w", werr)
			}
			written += int64(n)
			if progress != nil && total > 0 {
				pct := int(written * 100 / total)
				if pct != lastPct {
					lastPct = pct
					progress(pct, written, total)
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = f.Close()
			_ = os.Remove(dest)
			return fmt.Errorf("下载中断: %w", readErr)
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(dest)
		return err
	}

	// 完整性粗校验：非空 + MZ(PE) 头，防止下载到错误页面/空文件
	if written == 0 {
		_ = os.Remove(dest)
		return fmt.Errorf("下载内容为空")
	}
	if !isPEExecutable(dest) {
		_ = os.Remove(dest)
		return fmt.Errorf("下载的文件不是有效的可执行程序，已中止")
	}
	return nil
}

// Apply 用已下载的新文件替换当前运行中的 exe；relaunch 为真时自动启动新版
// 并退出当前进程（调用方应在此前完成会话收尾/持久化）。
//
// Windows 特征：运行中的 exe 不可删除/覆写，但允许改名，所以：
//
//	当前 exe -> <exe>.old
//	<downloaded> -> 当前 exe
//	（可选）启动新版 + os.Exit
//
// 旧文件 .old 留给新版启动时自行清理（见 main.go cleanupUpdateArtifacts）。
func (u *Updater) Apply(downloaded string, relaunch bool) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法定位当前程序: %w", err)
	}
	dir := filepath.Dir(self)
	final := filepath.Join(dir, filepath.Base(self))
	old := final + ".old"

	if err := os.Rename(self, old); err != nil {
		return fmt.Errorf("替换失败（旧程序无法改名）: %w", err)
	}
	if err := os.Rename(downloaded, final); err != nil {
		// 回滚：尽量恢复旧程序
		_ = os.Rename(old, self)
		return fmt.Errorf("替换失败（新程序无法落地）: %w", err)
	}

	if !relaunch {
		return nil
	}
	if err := startDetached(final); err != nil {
		return fmt.Errorf("程序已更新，但自动重启失败，请手动启动 %s: %w", final, err)
	}
	// 当前进程使命结束：退出（Unix 上旧 .old 文件同样由新版启动时清理）
	os.Exit(0)
	return nil
}

// startDetached 以独立进程启动新版（不继承当前控制台，避免窗口打扰）。
func startDetached(path string) error {
	cmd := exec.Command(path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			// CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS（std 包 syscall
			// 未导出该常量，这里用字面值）。
			CreationFlags: 0x00000200 | 0x00000008,
		}
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release() // 不等待，让新进程独立存活
	return nil
}

// isPEExecutable 检查文件是否以 Windows PE 头（"MZ"）开头。
func isPEExecutable(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 2)
	if _, err := io.ReadFull(f, b); err != nil {
		return false
	}
	return b[0] == 'M' && b[1] == 'Z'
}

// pickLatest 从全部 release 中挑出最新可用的 wails 正式版。
// 规则：
//   - tag 必须以 "-wails" 结尾且整体是合法版本号（v0.1 / v0.1.2 / v0.1-wails 均可）；
//   - 默认跳过 GitHub 预发布（CI 里 -pre/-rc 结尾的 tag 标为预发布）；
//   - 其余按版本号取最高。
func pickLatest(list []Release, includePre bool) (Release, bool) {
	var best Release
	bestSet := false
	for _, r := range list {
		if !isWailsTag(r.Tag) {
			continue
		}
		if r.Pre && !includePre {
			continue
		}
		if !isValidTag(r.Tag) {
			continue
		}
		if !bestSet || compareTags(r.Tag, best.Tag) > 0 {
			best, bestSet = r, true
		}
	}
	return best, bestSet
}

// isWailsTag 是否为 wails 版 tag（本项目约定：v<版本>-wails[-<预发布>]，
// 例如 v0.1-wails、v0.3-wails-rc；预发布与否由 GitHub prerelease 标志判定）。
func isWailsTag(tag string) bool {
	if !strings.HasPrefix(tag, "v") {
		return false
	}
	body := tag[1:] // 去掉 v
	// 数字段截止到第一个 '-'
	i := strings.IndexByte(body, '-')
	if i < 0 {
		return false // 没有版本后缀，不是 wails 版
	}
	numPart := body[:i]
	if !isValidTag("v" + numPart) {
		return false
	}
	suffix := body[i:] // 例如 "-wails"、" -wails-rc"、"-beta"
	if !strings.HasPrefix(suffix, "-wails") {
		return false // 版本号合法但没有 wails 标记（如 v0.2-beta 是 fyne 版）
	}
	rest := strings.TrimPrefix(suffix, "-wails")
	if rest == "" {
		return true // v0.2-wails
	}
	// 剩余必须是预发布后缀：如 -pre / -rc / -beta.1（须以 '-' 起头）
	return strings.HasPrefix(rest, "-") && isPrereleaseString(rest[1:])
}

// isPrereleaseString 预发布后缀字符集（与语义版本预发布一致：字母/数字/-/.）。
func isPrereleaseString(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-', c == '.':
		default:
			return false
		}
	}
	return true
}

// compareToCurrent 判断最新 tag 是否比本机版本新。
// 本机版本不是合法版本号（如手工构建的 "dev"）时一律视为可更新，
// 保证开发/调试版始终能收到正式版提示。
func compareToCurrent(current, latestTag string) bool {
	if !isValidTag(current) {
		return true
	}
	return compareTags(latestTag, current) > 0
}
