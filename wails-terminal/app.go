package main

// Wails 后端：会话列表 + ConPTY 终端管理
// 数据流：PTY 输出 -> base64 -> 前端事件 term:data；前端输入 -> TermWrite(base64) -> PTY

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"sync"

	"github.com/UserExistsError/conpty"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// SessionInfo 会话条目（前端渲染用）
type SessionInfo struct {
	ID   string `json:"id"`
	Dir  string `json:"dir"`
	Name string `json:"name"`
	Time string `json:"time"`
}

// App Wails 绑定对象
type App struct {
	ctx  context.Context
	mu   sync.Mutex
	term *conpty.ConPty
}

var (
	lastCols = 120 // 前端尚未连接时的默认终端尺寸
	lastRows = 32
)

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

func (a *App) shutdown(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.killLocked()
}

// killLocked 结束当前终端（调用方需持有 mu）
func (a *App) killLocked() {
	if a.term != nil {
		_ = a.term.Close() // ClosePseudoConsole 会顺带终止附着进程
		a.term = nil
	}
}

// ListSessions 扫描全部 claude 会话（复用 src/sessions.go 的解析逻辑）
func (a *App) ListSessions() []SessionInfo {
	list := scanAll()
	out := make([]SessionInfo, 0, len(list))
	for _, s := range list {
		out = append(out, SessionInfo{
			ID:   s.ID,
			Dir:  s.Dir,
			Name: displayName(s),
			Time: s.Time.Format("01-02 15:04"),
		})
	}
	return out
}

// StartSession 在 dir 目录恢复指定会话（claude -r <id>）
func (a *App) StartSession(id, dir string) error {
	return a.startPTY("cmd /c claude -r "+id, dir)
}

// StartNew 在 dir 目录启动全新会话
func (a *App) StartNew(dir string) error {
	return a.startPTY("cmd /c claude", dir)
}

// TermWrite 向前台终端写入输入（base64 编码的 UTF-8）
func (a *App) TermWrite(b64 string) {
	a.mu.Lock()
	p := a.term
	a.mu.Unlock()
	if p == nil {
		return
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return
	}
	_, _ = p.Write(raw)
}

// TermResize 调整伪终端尺寸（行列字符数）
func (a *App) TermResize(cols, rows int) {
	lastCols, lastRows = cols, rows
	a.mu.Lock()
	p := a.term
	a.mu.Unlock()
	if p != nil {
		_ = p.Resize(cols, rows)
	}
}

// TermKill 结束当前终端
func (a *App) TermKill() {
	a.mu.Lock()
	a.killLocked()
	a.mu.Unlock()
	runtime.EventsEmit(a.ctx, "term:exit", nil)
}

// startPTY 启动 ConPTY 并挂上输出转发 goroutine
func (a *App) startPTY(cmdLine, dir string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.killLocked()

	// 环境：去掉 NO_COLOR（claude 会因此关闭颜色），其余继承
	env := make([]string, 0, len(os.Environ())+1)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "NO_COLOR=") {
			env = append(env, e)
		}
	}
	env = append(env, "TERM=xterm-256color")

	pty, err := conpty.Start(cmdLine,
		conpty.ConPtyDimensions(lastCols, lastRows),
		conpty.ConPtyWorkDir(dir),
		conpty.ConPtyEnv(env),
	)
	if err != nil {
		return err
	}
	a.term = pty

	go func(p *conpty.ConPty) {
		buf := make([]byte, 8192)
		for {
			n, err := p.Read(buf)
			if n > 0 {
				// base64 传输，避免 JSON 破坏非 UTF-8 / 半截多字节序列
				runtime.EventsEmit(a.ctx, "term:data", base64.StdEncoding.EncodeToString(buf[:n]))
			}
			if err != nil {
				break
			}
		}
		a.mu.Lock()
		if a.term == p {
			a.term = nil
		}
		a.mu.Unlock()
		_ = p.Close()
		runtime.EventsEmit(a.ctx, "term:exit", nil)
	}(pty)
	return nil
}
