package main

// Wails 后端：会话列表 + 多会话 ConPTY 终端管理
// 数据流：PTY 输出 -> base64 -> 前端事件 term:data(token, b64)；
//         前端输入 -> TermWrite(token, b64) -> 对应会话的 PTY
// 多会话：terms 保存所有存活会话，切换会话不关闭旧的 ConPTY，
//         进程在后台保持运行。

import (
	"context"
	"encoding/base64"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"claude-terminal/internal/favorites"
	"claude-terminal/internal/session"

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

// ptyRef 包装 ConPTY，保证 Close 只执行一次。
// 背景：恢复新会话/关闭会话时，读协程退出后也会做收尾 Close；
// 若与 killLocked 路径直接调用 pty.Close() 竞争，同一 ConPTY 会被
// 关闭两次：第一次释放的句柄值很快被新会话的管道复用，第二次
// CloseHandle 会关掉新会话的活句柄，造成句柄复用冲突 / 堆损坏
// （0xc0000374）闪退。closed 标记把 Close 变成幂等操作，从根上消除。
type ptyRef struct {
	mu     sync.Mutex
	pty    *conpty.ConPty
	closed bool
}

// close 幂等关闭：无论被调用多少次，底层 ConPTY 只真正 Close 一次。
func (r *ptyRef) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.pty == nil {
		return
	}
	r.closed = true
	_ = r.pty.Close() // ClosePseudoConsole 会顺带终止附着进程
}

// isClosed 判断是否已关闭。
func (r *ptyRef) isClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

// write 写输入；已关闭时静默丢弃（避免写入已失效句柄）。
func (r *ptyRef) write(p []byte) (int, error) {
	r.mu.Lock()
	pty, closed := r.pty, r.closed
	r.mu.Unlock()
	if pty == nil || closed {
		return 0, nil
	}
	return pty.Write(p)
}

// resize 调整终端尺寸；已关闭时忽略。
func (r *ptyRef) resize(cols, rows int) error {
	r.mu.Lock()
	pty, closed := r.pty, r.closed
	r.mu.Unlock()
	if pty == nil || closed {
		return nil
	}
	return pty.Resize(cols, rows)
}

// pid 返回附着进程 PID（仅日志/调试用）。
func (r *ptyRef) pid() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pty == nil || r.closed {
		return -1
	}
	return r.pty.Pid()
}

// App Wails 绑定对象
type App struct {
	ctx   context.Context
	mu    sync.Mutex
	terms map[string]*ptyRef // token -> 会话终端（token：恢复会话=会话ID，新建="new-<时间戳>"）
}

var (
	lastCols = 120 // 前端尚未连接时的默认终端尺寸
	lastRows = 32
)

func NewApp() *App {
	return &App{terms: make(map[string]*ptyRef)}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.startAgentWatcher() // 常驻状态监视：变化即推送 agents:update 事件
}

func (a *App) shutdown(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, r := range a.terms {
		r.close() // 幂等
		delete(a.terms, k)
	}
}

// ListSessions 扫描全部 claude 会话（复用 internal/session 的解析逻辑）。
// 已软删除（hidden）的会话不返回；显示名优先本地别名。
func (a *App) ListSessions() []SessionInfo {
	st := favorites.Load()
	hidden := st.HiddenSet()
	aliases := st.Aliases
	list := session.ScanAll()
	out := make([]SessionInfo, 0, len(list))
	for _, s := range list {
		if hidden[s.ID] {
			continue
		}
		name := session.DisplayName(s)
		if n, ok := aliases[s.ID]; ok && n != "" {
			name = n
		}
		out = append(out, SessionInfo{
			ID:   s.ID,
			Dir:  s.Dir,
			Name: name,
			Time: s.Time.Format("01-02 15:04"),
		})
	}
	return out
}

// ListHiddenSessions 已软删除的会话（"已删"面板用，可恢复）
func (a *App) ListHiddenSessions() []SessionInfo {
	st := favorites.Load()
	hidden := st.HiddenSet()
	list := session.ScanAll()
	out := make([]SessionInfo, 0, len(list))
	for _, s := range list {
		if !hidden[s.ID] {
			continue
		}
		name := session.DisplayName(s)
		if n, ok := st.Aliases[s.ID]; ok && n != "" {
			name = n
		}
		out = append(out, SessionInfo{
			ID:   s.ID,
			Dir:  s.Dir,
			Name: name,
			Time: s.Time.Format("01-02 15:04"),
		})
	}
	return out
}

// StartSession 在 dir 目录恢复指定会话（claude -r <id>）。
// 若该会话已有活跃终端，直接返回其 token（前端切到对应标签页），
// 不重复启动进程。
func (a *App) StartSession(id, dir string) (string, error) {
	a.mu.Lock()
	r, ok := a.terms[id]
	a.mu.Unlock()
	if ok && !r.isClosed() {
		return id, nil // 已在运行，切回即可
	}
	if err := a.startPTY(id, "cmd /c claude -r "+id, dir); err != nil {
		return "", err
	}
	favorites.SaveLast(id) // 记住：下次启动自动恢复此会话
	return id, nil
}

// StartNew 在 dir 目录启动全新会话，返回新会话 token。
func (a *App) StartNew(dir string) (string, error) {
	token := "new-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := a.startPTY(token, "cmd /c claude", dir); err != nil {
		return "", err
	}
	return token, nil
}

// TermWrite 向指定会话终端写入输入（base64 编码的 UTF-8）
func (a *App) TermWrite(token, b64 string) {
	a.mu.Lock()
	r := a.terms[token]
	a.mu.Unlock()
	if r == nil {
		return
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return
	}
	_, _ = r.write(raw)
}

// TermResize 调整指定会话伪终端尺寸（行列字符数）
func (a *App) TermResize(token string, cols, rows int) {
	lastCols, lastRows = cols, rows
	a.mu.Lock()
	r := a.terms[token]
	a.mu.Unlock()
	if r != nil {
		_ = r.resize(cols, rows)
	}
}

// NotifyBeep 任务完成提示音（异步播放不阻塞 UI）。
// 双保险：先 MessageBeep 系统提示音（走系统声音方案，绝大多数机器能听到），
// 再 kernel32.Beep 三连上升音（有内置蜂鸣器硬件时更明显）。
func (a *App) NotifyBeep() {
	go func() {
		user32 := syscall.NewLazyDLL("user32.dll")
		msgBeep := user32.NewProc("MessageBeep")
		msgBeep.Call(0x40) // MB_ICONASTERISK：系统默认"提示"音

		k32 := syscall.NewLazyDLL("kernel32.dll")
		beep := k32.NewProc("Beep")
		for _, f := range []int{880, 1108, 1319} {
			beep.Call(uintptr(f), 140)
			time.Sleep(30 * time.Millisecond)
		}
	}()
}

// TermKill 结束指定会话终端（对应标签页关闭）
func (a *App) TermKill(token string) {
	a.mu.Lock()
	r, ok := a.terms[token]
	if ok {
		delete(a.terms, token)
	}
	a.mu.Unlock()
	if ok {
		r.close()
		runtime.EventsEmit(a.ctx, "term:exit", token)
	}
}

// startPTY 以 token 为键启动一个 ConPTY 会话并挂上输出转发 goroutine。
// 启动同 token 的新实例前会关闭旧实例（幂等，重启场景）。
func (a *App) startPTY(token, cmdLine, dir string) error {
	a.mu.Lock()
	if old, ok := a.terms[token]; ok {
		old.close()
		delete(a.terms, token)
	}
	a.mu.Unlock()

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
	ref := &ptyRef{pty: pty}

	a.mu.Lock()
	a.terms[token] = ref
	a.mu.Unlock()

	go func(token string, r *ptyRef) {
		buf := make([]byte, 8192)
		for {
			n, err := r.pty.Read(buf)
			if n > 0 {
				// base64 传输，避免 JSON 破坏非 UTF-8 / 半截多字节序列
				runtime.EventsEmit(a.ctx, "term:data", token, base64.StdEncoding.EncodeToString(buf[:n]))
			}
			if err != nil {
				break
			}
		}
		// 收尾：幂等关闭（若已被 TermKill/重启关闭则为空操作）
		r.close()
		// 只有自己仍是该 token 的当前实例时才发退出事件
		// （重启/关闭后旧实例的退出事件不应打扰新实例）
		a.mu.Lock()
		current := a.terms[token] == r
		if current {
			delete(a.terms, token)
		}
		a.mu.Unlock()
		if current {
			runtime.EventsEmit(a.ctx, "term:exit", token)
		}
	}(token, ref)
	return nil
}