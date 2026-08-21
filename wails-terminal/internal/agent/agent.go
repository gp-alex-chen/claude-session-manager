// Package agent 运行状态查询：claude agents --json（与 fyne-sidebar 同源）
// 徽标语义：kind=interactive -> 已打开（绿）；kind=background -> 后台运行中（橙）；
//           未出现 -> 未运行（灰）。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// —— 调试日志：写 exe 目录下 agents-debug.log ——
// 轮询的原始 JSON + 前端完成检测的每个决策都记录在这里，
// 排查"为什么没提示/没变化"时直接看文件即可，无需开 DevTools。
var debugMu sync.Mutex

func DebugLog(msg string) {
	debugMu.Lock()
	defer debugMu.Unlock()
	exe, err := os.Executable()
	if err != nil {
		return
	}
	dir := filepath.Dir(exe)
	f, err := os.OpenFile(filepath.Join(dir, "agents-debug.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	if st, _ := f.Stat(); st != nil && st.Size() > 600*1024 {
		_ = f.Truncate(0) // 防膨胀：超过 600KB 清空重写
	}
	_, _ = fmt.Fprintf(f, "[%s] %s\n", time.Now().Format("15:04:05.000"), msg)
}

// AgentInfo claude agents --json 的条目（实测字段：
//   state: working|done|blocked|queued?；status: busy|idle|waiting）
type AgentInfo struct {
	SessionID  string `json:"sessionId"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	State      string `json:"state"`
	Status     string `json:"status"`
	WaitingFor string `json:"waitingFor"`
	ID         string `json:"id"`
	PID        int    `json:"pid"`
	Cwd        string `json:"cwd"`
	StartedAt  int64  `json:"startedAt"`
}

func FetchFull() ([]AgentInfo, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cmd", "/c", "claude", "agents", "--json")
	// GUI 是 windowsgui 子系统（无控制台），子进程 cmd 默认会新开控制台窗口
	// 一闪而过（每次轮询闪一次）。CREATE_NO_WINDOW 让它在后台静默运行。
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	// 去掉 NO_COLOR，避免 claude 关闭颜色输出影响解析
	env := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "NO_COLOR=") {
			env = append(env, e)
		}
	}
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return nil, "ERR: " + err.Error()
	}
	var list []AgentInfo
	if json.Unmarshal(out, &list) != nil {
		return nil, "ERR unmarshal: " + string(out)
	}
	return list, string(out)
}

// —— 常驻状态监视器（实时方案，替代前端轮询） ——
// claude CLI 的 agents --json 是一次性快照、无 watch 模式；
// agent 状态（尤其 interactive 的 busy/idle）在 claude daemon 内存里
// 不落盘，文件监听也覆盖不全。所以由 App 层起常驻 goroutine：每 1~2s
// 拉一次快照，对比变化后立即以 agents:update 事件推给前端。
// 缓存变量由 App 层读写（Mu 保护）。
var (
	Mu     sync.RWMutex
	Cache  []AgentInfo
	Sig    string // 上次推送时活跃集合的签名（变化检测）
	Active bool   // 上次快照是否有活跃会话（决定下一次检测间隔）
)

// Signature 活跃集合签名：sessionId+state+status+kind 排序拼接，
// 避免 JSON 字段顺序/时间戳噪声触发误判变化。
func Signature(list []AgentInfo) string {
	parts := make([]string, 0, len(list))
	for _, a := range list {
		parts = append(parts, a.SessionID+"|"+a.State+"|"+a.Status+"|"+a.Kind)
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}