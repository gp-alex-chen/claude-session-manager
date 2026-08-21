package main

// App 的 agents 相关绑定方法（数据层在 internal/agent）。

import (
	"strconv"
	"time"

	"claude-terminal/internal/agent"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// startAgentWatcher 启动常驻监视器（app.startup 中调用）。
func (a *App) startAgentWatcher() {
	go func() {
		fail := 0
		for {
			list, raw := agent.FetchFull()

			var interval time.Duration
			if list == nil {
				fail++
				if fail >= 3 {
					interval = 5 * time.Second // 连续失败：降频重试
				} else {
					interval = 2 * time.Second
				}
			} else {
				fail = 0
				sig := agent.Signature(list)
				active := len(list) > 0
				agent.Mu.Lock()
				changed := sig != agent.Sig
				if len(list) > 0 {
					agent.DebugLog("watch raw=" + raw) // 有内容时才记原始 JSON，防日志膨胀
				}
				agent.Cache = list
				agent.Sig = sig
				agent.Active = active
				agent.Mu.Unlock()
				if changed {
					agent.DebugLog("状态变化，push agents:update n=" + strconv.Itoa(len(list)))
					runtime.EventsEmit(a.ctx, "agents:update", list)
				}
				if active {
					interval = 1 * time.Second // 有会话在跑：高频检测（≈实时）
				} else {
					interval = 2 * time.Second // 空闲：低一些，仍秒级
				}
			}
			time.Sleep(interval)
		}
	}()
}

// GetAgents 返回最近一次监视器缓存（近实时、零子进程开销）；
// 首次调用时缓存未就绪则同步拉取一次。
func (a *App) GetAgents() []agent.AgentInfo {
	agent.Mu.RLock()
	c := agent.Cache
	sig := agent.Sig
	agent.Mu.RUnlock()
	if sig == "" {
		list, raw := agent.FetchFull()
		agent.Mu.Lock()
		agent.Cache = list
		agent.Sig = agent.Signature(list)
		agent.Active = len(list) > 0
		agent.Mu.Unlock()
		agent.DebugLog("GetAgents 首次拉取 len=" + strconv.Itoa(len(list)) + " raw=" + raw)
		return list
	}
	return c
}

// DebugLog 前端诊断日志（完成检测的每个决策，排查"无提示"用）
func (a *App) DebugLog(msg string) {
	agent.DebugLog(msg)
}
