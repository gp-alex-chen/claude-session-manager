package main

// App 的更新相关绑定方法：一键"检查更新 / 更新到最新版"。
// 数据层在 internal/updater；这里负责：
//   - 组装更新器（本仓库 + claude-terminal.exe 资产）
//   - 下载进度/状态经事件推给前端（update:state / update:progress）
//   - 更新前持久化打开的会话并关闭所有 ConPTY，再替换+自动重启

import (
	"context"
	"fmt"
	"os"
	"time"

	"claude-terminal/internal/updater"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// 更新源常量：与本仓库 GitHub Releases 约定一致
// （CI：打 v*-wails tag 时构建并发布 claude-terminal.exe）。
const (
	updateOwner = "gp-alex-chen"
	updateRepo  = "claude-session-manager"
	updateAsset = "claude-terminal.exe"

	// updateCheckTimeout 一次检查/下载的上下文超时
	updateCheckTimeout    = 15 * time.Second
	updateDownloadTimeout = 5 * time.Minute
)

// GetVersion 返回当前版本（-ldflags -X main.Version=<tag> 注入；
// 未注入时为 "dev"，表示手工/开发构建）。
func (a *App) GetVersion() string { return Version }

// CheckForUpdate 手动检查更新：查询 GitHub Releases 里最新 wails 正式版。
// 返回 updater.Info（前端据此展示"发现新版本/已是最新"）。
func (a *App) CheckForUpdate() (*updater.Info, error) {
	u := a.updater()
	ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
	defer cancel()
	return u.Check(ctx)
}

// UpdateToLatest 下载最新版并替换自身，随后自动重启进新版。
// 由前端"更新"按钮触发；成功后本进程退出（前端以事件收到结果）。
// 注意：重启会结束当前所有 ConPTY 会话进程（已先持久化清单供新版恢复）。
func (a *App) UpdateToLatest() error {
	ctx, cancel := context.WithTimeout(context.Background(), updateDownloadTimeout)
	defer cancel()

	u := a.updater()
	info, err := u.Check(ctx)
	if err != nil {
		runtime.EventsEmit(a.ctx, "update:state", "检查失败")
		return err
	}
	if !info.HasUpdate {
		return fmt.Errorf("已是最新版本 %s", info.Latest)
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法定位当前程序: %w", err)
	}
	tmp := self + ".new"
	_ = os.Remove(tmp) // 清掉上次可能残留的下载

	runtime.EventsEmit(a.ctx, "update:state", "下载中")
	if err := u.DownloadTo(ctx, info, tmp, func(pct int, _, _ int64) {
		runtime.EventsEmit(a.ctx, "update:progress", pct)
	}); err != nil {
		_ = os.Remove(tmp)
		runtime.EventsEmit(a.ctx, "update:state", "下载失败")
		return err
	}
	runtime.EventsEmit(a.ctx, "update:state", "正在替换程序")
	runtime.EventsEmit(a.ctx, "update:progress", 100)

	// 更新前收尾：持久化"当前打开的会话"（供新版恢复），再关闭全部 ConPTY
	// （关闭 = claude 进程终止；闭眼前不再等待任务，符合"立即生效"的用户预期）
	a.persistOpenSessions()
	a.closeAllTerms()

	runtime.EventsEmit(a.ctx, "update:state", "重启中")
	if err := u.Apply(tmp, true); err != nil {
		runtime.EventsEmit(a.ctx, "update:state", "更新失败")
		return err
	}
	return nil // 正常情况下不会走到：Apply 成功会启动新版并 os.Exit
}

// updater 组装本项目更新器。
func (a *App) updater() *updater.Updater {
	return updater.New(updateOwner, updateRepo, updateAsset, Version)
}

// cleanupUpdateArtifacts 清理更新器留下的残留文件（在主函数启动时调用）：
//   - <exe>.old：上次自替换成功后旧版程序（新版已跑起来，旧文件不再需要）；
//   - <exe>.new：上次下载被中断/失败留下的临时文件。
func cleanupUpdateArtifacts() {
	if self, err := os.Executable(); err == nil {
		_ = os.Remove(self + ".old")
		_ = os.Remove(self + ".new")
	}
}

// closeAllTerms 关闭全部 ConPTY 会话（与 shutdown 相同的幂等路径，
// 仅用于"更新替换前"的主动收尾）。
func (a *App) closeAllTerms() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, r := range a.terms {
		r.close()
		delete(a.terms, k)
	}
}
