package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Open is injectable so ordinary tests cannot read real Codex state or start
// a CLI. Production owns only its short-lived metadata stdio server.
type CodexRPC struct {
	Open func(context.Context) (io.ReadWriteCloser, error)
}

func NewCodexRPC(lookPath func(string) (string, error)) *CodexRPC {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	return &CodexRPC{Open: func(ctx context.Context) (io.ReadWriteCloser, error) {
		bin, err := ResolveCodexExecutable(lookPath)
		if err != nil {
			return nil, err
		}
		return openCodexRPCProcess(ctx, bin, nil)
	}}
}

func openCodexRPCProcess(ctx context.Context, bin string, env []string) (io.ReadWriteCloser, error) {
	cmd := exec.CommandContext(ctx, bin, "app-server", "--listen", "stdio://")
	cmd.Env = env
	configureRPCProcess(cmd)
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, err
	}
	return &processConnection{Reader: stdout, Writer: stdin, cmd: cmd}, nil
}

// Native execution avoids leaving cmd/npm child processes behind on timeout.
// Native PATH installs and the layouts shipped by the official npm launcher
// are supported; unknown shims fail explicitly rather than guessing commands.
func ResolveCodexExecutable(lookPath func(string) (string, error)) (string, error) {
	path, err := lookPath("codex")
	if err != nil {
		return "", fmt.Errorf("未检测到 Codex CLI，请先安装并加入 PATH: %w", err)
	}
	if strings.EqualFold(filepath.Ext(path), ".exe") {
		return path, nil
	}
	arch, target := "x64", "x86_64-pc-windows-msvc"
	if runtime.GOARCH == "arm64" {
		arch, target = "arm64", "aarch64-pc-windows-msvc"
	}
	root := filepath.Join(filepath.Dir(path), "node_modules", "@openai", "codex")
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	roots := []string{filepath.Join(root, "node_modules", "@openai", "codex-win32-"+arch, "vendor"), filepath.Join(root, "vendor"), filepath.Join(filepath.Dir(root), "codex-win32-"+arch, "vendor")}
	for _, vendor := range roots {
		for _, sub := range []string{"bin", "codex"} {
			candidate := filepath.Join(vendor, target, sub, "codex.exe")
			if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
				return candidate, nil
			}
		}
	}
	return "", errors.New("找到了 Codex 启动脚本，但未找到原生 codex.exe；请检查官方 CLI 安装")
}

type processConnection struct {
	io.Reader
	io.Writer
	cmd  *exec.Cmd
	once sync.Once
}

func (p *processConnection) Close() error {
	p.once.Do(func() {
		p.Writer.(io.Closer).Close()
		done := make(chan struct{})
		go func() { _ = p.cmd.Wait(); close(done) }()
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			_ = p.cmd.Process.Kill()
			<-done
		}
		p.Reader.(io.Closer).Close()
	})
	return nil
}

type rpcMessage struct {
	ID     *int            `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}
type rpcRead struct {
	msg rpcMessage
	err error
}
type rpcSession struct {
	ctx    context.Context
	conn   io.ReadWriteCloser
	reads  <-chan rpcRead
	nextID int
}

func newRPCSession(ctx context.Context, conn io.ReadWriteCloser) *rpcSession {
	reads := make(chan rpcRead)
	go func() {
		defer close(reads)
		sc := bufio.NewScanner(conn)
		sc.Buffer(make([]byte, 8192), 16<<20)
		for sc.Scan() {
			var msg rpcMessage
			err := json.Unmarshal(sc.Bytes(), &msg)
			select {
			case reads <- rpcRead{msg, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
		err := sc.Err()
		if err == nil {
			err = io.EOF
		}
		select {
		case reads <- rpcRead{err: err}:
		case <-ctx.Done():
		}
	}()
	return &rpcSession{ctx: ctx, conn: conn, reads: reads}
}

func (r *rpcSession) call(method string, params any, result any) error {
	r.nextID++
	id := r.nextID
	request, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	if _, err = r.conn.Write(append(request, '\n')); err != nil {
		return err
	}
	for {
		select {
		case <-r.ctx.Done():
			return r.ctx.Err()
		case read, ok := <-r.reads:
			if !ok {
				return io.EOF
			}
			if read.err != nil {
				return fmt.Errorf("Codex protocol: %w", read.err)
			}
			m := read.msg
			if m.ID == nil {
				continue
			}
			if m.Method != "" {
				return errors.New("unexpected Codex server request during metadata operation")
			}
			if *m.ID != id {
				return errors.New("unexpected Codex response ID")
			}
			if m.Error != nil {
				return fmt.Errorf("Codex %s: %s", method, m.Error.Message)
			}
			if len(m.Result) == 0 || string(m.Result) == "null" {
				return errors.New("missing Codex result")
			}
			return json.Unmarshal(m.Result, result)
		}
	}
}

func (c *CodexRPC) ListSessions(parent context.Context) ([]Session, error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	conn, err := c.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	r := newRPCSession(ctx, conn)
	var init map[string]any
	if err := r.call("initialize", map[string]any{"clientInfo": map[string]string{"name": "claude_codex_session_manager", "version": "1"}}, &init); err != nil {
		return nil, err
	}
	if _, err := conn.Write([]byte("{\"method\":\"initialized\"}\n")); err != nil {
		return nil, err
	}
	var out []Session
	cursor := ""
	seenCursors := map[string]bool{}
	seenIDs := map[string]bool{}
	for page := 0; page < 1000; page++ {
		params := map[string]any{"limit": 100, "archived": false, "sortKey": "updated_at", "sourceKinds": []string{"cli", "vscode", "appServer"}, "modelProviders": []string{}}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var response struct {
			Data *[]struct {
				ID, Cwd, Name, Preview string
				UpdatedAt              int64   `json:"updatedAt"`
				ParentThreadID         *string `json:"parentThreadId"`
				Ephemeral              bool
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := r.call("thread/list", params, &response); err != nil {
			return nil, err
		}
		if response.Data == nil {
			return nil, errors.New("Codex history result has no data array")
		}
		for _, s := range *response.Data {
			ref := SessionRef{Codex, s.ID}
			if validateRef(ref) != nil || s.Ephemeral || s.ParentThreadID != nil || strings.TrimSpace(s.Cwd) == "" || seenIDs[s.ID] {
				continue
			}
			seenIDs[s.ID] = true
			name := strings.TrimSpace(s.Name)
			if name == "" {
				name = strings.TrimSpace(s.Preview)
			}
			if name == "" {
				name = "Codex · " + s.ID[:8]
			}
			// Keep metadata labels bounded; never render them as HTML.
			runes := []rune(name)
			if len(runes) > 120 {
				name = string(runes[:120]) + "…"
			}
			out = append(out, Session{ref, s.Cwd, name, time.Unix(s.UpdatedAt, 0)})
		}
		if response.NextCursor == nil || *response.NextCursor == "" {
			return out, nil
		}
		cursor = *response.NextCursor
		if seenCursors[cursor] {
			return nil, errors.New("Codex history pagination loop")
		}
		seenCursors[cursor] = true
	}
	return nil, errors.New("Codex history exceeded pagination limit")
}

// Rename changes persisted metadata without resuming or starting a model turn.
func (c *CodexRPC) Rename(parent context.Context, id, name string) error {
	if err := validateRef(SessionRef{Codex, id}); err != nil {
		return err
	}
	var err error
	name, err = ValidateTitle(name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	conn, err := c.Open(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	r := newRPCSession(ctx, conn)
	var result map[string]any
	if err := r.call("initialize", map[string]any{"clientInfo": map[string]string{"name": "claude_codex_session_manager", "version": "1"}}, &result); err != nil {
		return err
	}
	if _, err := conn.Write([]byte("{\"method\":\"initialized\"}\n")); err != nil {
		return err
	}
	return r.call("thread/name/set", map[string]string{"threadId": id, "name": name}, &result)
}
