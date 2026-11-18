package app

// App 的更新相关绑定方法：一键"检查更新 / 更新到最新版"。
// 数据层在 internal/updater；这里负责：
//   - 组装更新器（本仓库 + TheOne.exe 资产）
//   - 下载进度/状态经事件推给前端（update:state / update:progress）
//   - 更新前持久化打开的会话并关闭所有 ConPTY，再替换+自动重启

import (
	"context"
	"fmt"
	"github.com/gp-alex-chen/claude-session-manager/internal/provider"
	"os"
	"path/filepath"
	"time"

	"github.com/gp-alex-chen/claude-session-manager/internal/updater"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// 更新源常量：与本仓库 GitHub Releases 约定一致
// （CI：打 v*-wails tag 时构建并发布 TheOne.exe）。
const (
	updateOwner = "gp-alex-chen"
	updateRepo  = "claude-session-manager"
	updateAsset = "TheOne.exe"

	// updateCheckTimeout 一次检查/下载的上下文超时
	updateCheckTimeout    = 15 * time.Second
	updateDownloadTimeout = 5 * time.Minute
)

// GetVersion 返回当前版本（-ldflags -X github.com/gp-alex-chen/claude-session-manager/internal/app.Version=<tag> 注入；
// 未注入时为 "dev"，表示手工/开发构建）。
// CheckForUpdate 手动检查更新：查询 GitHub Releases 里最新 wails 正式版。
// 返回 updater.Info（前端据此展示版本、更新说明以及"发现新版本/已是最新"）。
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
	runtimeCtx := a.runtimeContext()

	u := a.updater()
	info, err := u.Check(ctx)
	if err != nil {
		runtime.EventsEmit(runtimeCtx, "update:state", "检查失败")
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

	runtime.EventsEmit(runtimeCtx, "update:state", "下载中")
	if err := u.DownloadTo(ctx, info, tmp, func(pct int, _, _ int64) {
		runtime.EventsEmit(runtimeCtx, "update:progress", pct)
	}); err != nil {
		_ = os.Remove(tmp)
		runtime.EventsEmit(runtimeCtx, "update:state", "下载失败")
		return err
	}
	runtime.EventsEmit(runtimeCtx, "update:state", "正在替换程序")
	runtime.EventsEmit(runtimeCtx, "update:progress", 100)

	// 更新前收尾：持久化"当前打开的会话"（供新版恢复），再关闭全部 ConPTY
	// （关闭 = claude 进程终止；闭眼前不再等待任务，符合"立即生效"的用户预期）
	if err := a.applyDownloadedUpdate(tmp, u.Apply, func() {
		runtime.EventsEmit(runtimeCtx, "update:state", "重启中")
	}); err != nil {
		runtime.EventsEmit(runtimeCtx, "update:state", "更新失败")
		return err
	}
	return nil // 正常情况下不会走到：Apply 成功会启动新版并 os.Exit
}

func (a *App) applyDownloadedUpdate(downloaded string, apply func(string, bool) error, onReady func()) error {
	generation, done, owner := a.claimStop()
	if !owner {
		return fmt.Errorf("应用正在关闭或已经关闭")
	}
	defer a.completeStop(generation, done, true)
	a.cancelAgentLaunches(generation)
	refs := a.antigravityRestoreReferences()
	a.launchMu.Lock()
	defer a.launchMu.Unlock()
	a.lifecycleMu.Lock()
	current := a.lifecycleID == generation && a.stopping
	a.lifecycleMu.Unlock()
	if !current {
		return fmt.Errorf("应用生命周期已变化，更新已取消")
	}
	if err := a.finishTerminals(refs, generation, true); err != nil {
		return fmt.Errorf("无法保存会话恢复清单，已取消更新: %w", err)
	}
	a.closeProviderServices()
	if onReady != nil {
		onReady()
	}
	err := apply(downloaded, true)
	// Apply normally exits. A failed replacement leaves this instance usable
	// so opening a terminal can lazily start a fresh shared service.
	if err != nil {
		a.beginProviderServices()
	}
	return err
}

// updater 组装本项目更新器。
func (a *App) updater() *updater.Updater {
	return updater.New(updateOwner, updateRepo, updateAsset, Version)
}

// cleanupUpdateArtifacts 清理更新器留下的残留文件（在主函数启动时调用）：
//   - <exe>.old：旧版兼容残留；
//   - <exe>.update-*.old：带随机后缀的旧版程序；
//   - <exe>.new：上次下载被中断/失败留下的临时文件。
func CleanupUpdateArtifacts() {
	if self, err := os.Executable(); err == nil {
		_ = os.Remove(self + ".old")
		if backups, globErr := filepath.Glob(self + ".update-*.old"); globErr == nil {
			for _, backup := range backups {
				_ = os.Remove(backup)
			}
		}
		_ = os.Remove(self + ".new")
	}
}

type codexUpdateReferencesSource interface {
	CaptureUpdateReferences(context.Context) (map[string]string, func(bool), error)
}

func (a *App) finishTerminals(refs map[string]string, generation uint64, updating bool) error {
	if a.terms == nil {
		return nil
	}
	a.agentLaunchMu.Lock()
	defer a.agentLaunchMu.Unlock()
	carry := a.pendingStartupRestoreReferences(generation)
	if p, err := a.providers.Lookup(provider.Antigravity); err == nil {
		if source, ok := p.(antigravityRestoreSource); ok {
			refs = source.FilterRestoreReferences(refs)
		}
	}
	a.adoptionMu.Lock()
	defer a.adoptionMu.Unlock()
	pending := append(carry, a.pendingAdoptionIDsLocked()...)
	forget := a.cancelledAdoptionTokensLocked()
	if !updating {
		return a.terms.CloseAllWithNativeReferences(pending, forget, refs)
	}
	ctx := a.runtimeContext()
	return a.terms.SaveAndCloseForUpdate(pending, forget, refs, func() (map[string]string, func(bool)) {
		p, _ := a.providers.Lookup(provider.Codex)
		source, ok := p.(codexUpdateReferencesSource)
		if !ok {
			return nil, nil
		}
		bindings, finish, err := source.CaptureUpdateReferences(ctx)
		if err != nil {
			a.log("更新前确认 Codex 原生会话失败: " + err.Error())
		}
		return bindings, finish
	})
}
