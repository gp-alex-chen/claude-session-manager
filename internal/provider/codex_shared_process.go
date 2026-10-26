package provider

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var sharedListenLine = regexp.MustCompile(`(?m)^[ \t]*listening on:[ \t]*(ws://127\.0\.0\.1:[1-9][0-9]*)[ \t]*\r?\n`)

type sharedAddressWriter struct {
	mu    sync.Mutex
	tail  string
	ready chan string
}

func (w *sharedAddressWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tail += string(data)
	if match := sharedListenLine.FindStringSubmatch(w.tail); len(match) == 2 {
		select {
		case w.ready <- match[1]:
		default:
		}
	}
	if len(w.tail) > 4096 {
		w.tail = w.tail[len(w.tail)-4096:]
	}
	return len(data), nil
}

func startSharedCodexProcess(parent context.Context, bin, dir string, env, args []string, generation uint64, cleanup func()) (*sharedCodexInstance, error) {
	if err := parent.Err(); err != nil {
		return nil, err
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(nonce[:])
	digest := sha256.Sum256([]byte(token))
	ctx, cancel := context.WithCancel(parent)
	command := append([]string{"app-server", "--listen", "ws://127.0.0.1:0", "--ws-auth", "capability-token", "--ws-token-sha256", hex.EncodeToString(digest[:])}, args...)
	cmd := exec.CommandContext(ctx, bin, command...)
	cmd.Env = env
	cmd.Dir = dir
	cmd.Stdout = io.Discard
	address := &sharedAddressWriter{ready: make(chan string, 1)}
	cmd.Stderr = address
	cmd.WaitDelay = 2 * time.Second
	stop, err := startSharedCodexCommand(cmd)
	if err != nil {
		cancel()
		return nil, err
	}
	done := make(chan struct{})
	var closeOnce sync.Once
	closeProcess := func() { closeOnce.Do(func() { cancel(); stop() }); <-done }
	instance := &sharedCodexInstance{generation: generation, pid: cmd.Process.Pid, executable: bin, token: token, done: done, close: closeProcess}
	go func() {
		_ = cmd.Wait()
		stop()
		if cleanup != nil {
			cleanup()
		}
		close(done)
	}()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case instance.url = <-address.ready:
	case <-ctx.Done():
		closeProcess()
		return nil, ctx.Err()
	case <-done:
		return nil, errors.New("共享 Codex 服务启动失败，请检查原生 CLI 和配置")
	case <-timer.C:
		closeProcess()
		return nil, errors.New("共享 Codex 服务启动超时")
	}
	instance.connect = func(ctx context.Context) (io.ReadWriteCloser, error) {
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, instance.url, http.Header{"Authorization": []string{"Bearer " + token}})
		if err != nil {
			return nil, fmt.Errorf("连接共享 Codex 服务失败：%w", err)
		}
		conn.SetReadLimit(codexRelayMessageLimit)
		return &codexWebSocketStream{conn: conn}, nil
	}
	return instance, nil
}

// Adapt one dedicated WebSocket connection to JSONL. Closing it only detaches
// this client. It does not own the shared process or another client's stream.
type codexWebSocketStream struct {
	conn    *websocket.Conn
	pending []byte
	writeMu sync.Mutex
}

func (c *codexWebSocketStream) Read(p []byte) (int, error) {
	for len(c.pending) == 0 {
		kind, data, err := c.conn.ReadMessage()
		if err != nil {
			return 0, err
		}
		if kind != websocket.TextMessage {
			return 0, errors.New("Codex 返回了非文本帧")
		}
		c.pending = append(data, '\n')
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}
func (c *codexWebSocketStream) Write(data []byte) (int, error) {
	body := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if strings.ContainsAny(body, "\r\n") {
		return 0, errors.New("Codex 消息必须是单个 JSON 对象")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.WriteMessage(websocket.TextMessage, []byte(body)); err != nil {
		return 0, err
	}
	return len(data), nil
}
func (c *codexWebSocketStream) Close() error { return c.conn.Close() }
