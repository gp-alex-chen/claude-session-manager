package provider

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	codexTokenEnv = "FYNE_CODEX_TRANSPORT_TOKEN"
	// Remote plugin catalogs can exceed 16 MiB. Bound both directions by
	// JSON payload size, excluding stdio's LF or CRLF delimiter.
	codexRelayMessageLimit = 64 << 20
)

// NewCodexRelayFactory creates one app-owned stdio server for one native TUI.
// The local WebSocket relay is authenticated, accepts one client, and observes
// but does not alter messages. Native approval requests go directly to the TUI.
func NewCodexRelayFactory(lookPath func(string) (string, error)) CodexRelayFactory {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	return newCodexRelayFactory(lookPath, nil)
}

func newCodexRelayFactory(lookPath func(string) (string, error), env []string) CodexRelayFactory {
	return codexRelayFactoryWithStderr(lookPath, env, io.Discard)
}

func codexRelayFactoryWithStderr(lookPath func(string) (string, error), env []string, stderr io.Writer) CodexRelayFactory {
	return func(ctx context.Context, dir string, client, server func([]byte), disconnect func()) (LaunchSpec, error) {
		if err := ctx.Err(); err != nil {
			return LaunchSpec{}, err
		}
		bin, err := ResolveCodexExecutable(lookPath)
		if err != nil {
			return LaunchSpec{}, err
		}
		// The PTY owns this server's lifetime. Wails can cancel its context
		// before Shutdown captures the restore snapshot, so cancellation of
		// that context must not terminate a still-owned terminal early.
		cmdCtx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(cmdCtx, bin, "app-server", "--listen", "stdio://")
		cmd.Env = env
		configureRPCProcess(cmd)
		cmd.Dir = dir
		cmd.Stderr = stderr
		in, err := cmd.StdinPipe()
		if err != nil {
			cancel()
			return LaunchSpec{}, err
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			in.Close()
			cancel()
			return LaunchSpec{}, err
		}
		if err := cmd.Start(); err != nil {
			in.Close()
			out.Close()
			cancel()
			return LaunchSpec{}, err
		}
		process := &processConnection{Reader: out, Writer: in, cmd: cmd}
		relay, err := newCodexRelay(cmdCtx, process, client, server, disconnect)
		if err != nil {
			cancel()
			process.Close()
			return LaunchSpec{}, err
		}
		if err := ctx.Err(); err != nil {
			cancel()
			relay.Close()
			return LaunchSpec{}, err
		}
		// Keep the capability out of argv, logs and persistent files.
		return LaunchSpec{Command: "codex", Args: []string{"--remote", relay.url, "--remote-auth-token-env", codexTokenEnv}, Env: []string{codexTokenEnv + "=" + relay.token}, Close: func() { cancel(); relay.Close() }}, nil
	}
}

type codexRelay struct {
	conn                           io.ReadWriteCloser
	http                           *http.Server
	url, token                     string
	clientObserver, serverObserver func([]byte)
	disconnect                     func()
	mu                             sync.Mutex
	socket                         *websocket.Conn
	used, closed                   bool
	once                           sync.Once
	done                           chan struct{}
}

func newCodexRelay(ctx context.Context, conn io.ReadWriteCloser, client, server func([]byte), disconnect func()) (*codexRelay, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	r := &codexRelay{conn: conn, url: "ws://" + listener.Addr().String(), token: hex.EncodeToString(bytes), clientObserver: client, serverObserver: server, disconnect: disconnect, done: make(chan struct{})}
	r.http = &http.Server{Handler: http.HandlerFunc(r.handle), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = r.http.Serve(listener) }()
	go func() {
		select {
		case <-ctx.Done():
			r.Close()
		case <-r.done:
		}
	}()
	return r, nil
}

func (r *codexRelay) handle(w http.ResponseWriter, req *http.Request) {
	expected := "Bearer " + r.token
	if req.Method != "GET" || req.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(req.Header.Get("Authorization")), []byte(expected)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	r.mu.Lock()
	if r.closed || r.used {
		r.mu.Unlock()
		http.Error(w, "terminal already connected", http.StatusConflict)
		return
	}
	r.used = true
	r.mu.Unlock()
	ws, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, req, nil)
	if err != nil {
		r.Close()
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		ws.Close()
		return
	}
	r.socket = ws
	r.mu.Unlock()
	ws.SetReadLimit(codexRelayMessageLimit)
	defer r.Close()
	go func() {
		scanner := bufio.NewScanner(r.conn)
		scanner.Buffer(make([]byte, 8192), codexRelayMessageLimit+2)
		for scanner.Scan() {
			data := scanner.Bytes()
			if len(data) > codexRelayMessageLimit {
				break
			}
			if r.serverObserver != nil {
				r.serverObserver(append([]byte(nil), data...))
			}
			_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if ws.WriteMessage(websocket.TextMessage, data) != nil {
				break
			}
		}
		r.Close()
	}()
	for {
		kind, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		// Stdio is newline framed. Native messages are single JSON objects.
		if kind != websocket.TextMessage || strings.ContainsAny(string(data), "\r\n") {
			return
		}
		if r.clientObserver != nil {
			r.clientObserver(append([]byte(nil), data...))
		}
		if _, err = r.conn.Write(append(data, '\n')); err != nil {
			return
		}
	}
}

func (r *codexRelay) Close() {
	r.once.Do(func() {
		r.mu.Lock()
		r.closed = true
		ws := r.socket
		r.mu.Unlock()
		_ = r.http.Close()
		if ws != nil {
			_ = ws.Close()
		}
		_ = r.conn.Close()
		if r.disconnect != nil {
			r.disconnect()
		}
		close(r.done)
	})
}
