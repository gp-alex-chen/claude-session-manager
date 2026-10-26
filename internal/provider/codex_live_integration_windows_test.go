//go:build windows && integration

package provider

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UserExistsError/conpty"
)

// The native TUI uses synthetic auth and history. No model turn is sent.
// This checks the real --remote argument order, handshake, identity and cleanup.
func TestCodexNativeRemoteTUIIntegration(t *testing.T) {
	for _, mode := range []string{"new", "resume"} {
		t.Run(mode, func(t *testing.T) {
			bin, home, dir, id, env := codexIntegrationFixture(t)
			if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"OPENAI_API_KEY":"TEST_NOT_REAL_NO_MODEL_TURN"}`), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			changed := make(chan struct{}, 1)
			lookup := func(string) (string, error) { return bin, nil }
			live := NewCodexLive(NewCodex(nil), newCodexRelayFactory(lookup, env), func() {
				select {
				case changed <- struct{}{}:
				default:
				}
			})
			req := OpenRequest{Dir: dir, RuntimeToken: "new-codex-integration"}
			if mode == "resume" {
				req.Ref = &SessionRef{Codex, id}
			}
			spec, err := live.PrepareLaunch(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			defer spec.Close()
			command := "cmd /d /c codex " + strings.Join(spec.Args, " ")
			tuiEnv := []string{}
			for _, value := range env {
				key, _, _ := strings.Cut(value, "=")
				if !strings.EqualFold(key, "TERM") && !strings.EqualFold(key, "NO_COLOR") && !strings.EqualFold(key, "PATH") {
					tuiEnv = append(tuiEnv, value)
				}
			}
			tuiEnv = append(tuiEnv, spec.Env...)
			tuiEnv = append(tuiEnv, "TERM=xterm-256color")
			p, err := conpty.Start(command, conpty.ConPtyWorkDir(dir), conpty.ConPtyDimensions(100, 30), conpty.ConPtyEnv(tuiEnv))
			if err != nil {
				t.Fatal(err)
			}
			var closePTY sync.Once
			closeTerminal := func() { closePTY.Do(func() { _ = p.Close() }) }
			defer closeTerminal()
			go func() {
				buf := make([]byte, 8192)
				for {
					n, err := p.Read(buf)
					if err != nil {
						return
					}
					text := string(buf[:n])
					if strings.Contains(text, "\x1b[6n") {
						p.Write([]byte("\x1b[1;1R"))
					}
					if strings.Contains(text, "\x1b[c") {
						p.Write([]byte("\x1b[?1;2c"))
					}
				}
			}()
			var nativeID string
			for {
				statuses, _ := live.Status(ctx)
				if len(statuses) == 1 && statuses[0].State == "idle" {
					if mode == "resume" && statuses[0].SessionID != id {
						t.Fatal("wrong resumed identity", statuses)
					}
					if validateRef(SessionRef{Codex, statuses[0].SessionID}) != nil {
						t.Fatal("invalid native identity", statuses)
					}
					nativeID = statuses[0].SessionID
					break
				}
				select {
				case <-changed:
				case <-ctx.Done():
					t.Fatalf("native %s TUI did not bind authoritative thread: %v", mode, ctx.Err())
				}
			}
			t.Log("native thread bound; closing terminal-owned resources")
			closeTerminal()
			spec.Close()
			// ID allocation alone is not durable: empty new threads have no
			// rollout, while resumed native history must remain resumable.
			c := &CodexRPC{Open: func(ctx context.Context) (io.ReadWriteCloser, error) { return openCodexRPCProcess(ctx, bin, env) }}
			conn, err := c.Open(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			r := newRPCSession(ctx, conn)
			var initialized map[string]any
			if err := r.call("initialize", map[string]any{"clientInfo": map[string]string{"name": "session_restore_test", "version": "test"}}, &initialized); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Write([]byte("{\"method\":\"initialized\"}\n")); err != nil {
				t.Fatal(err)
			}
			var resumed struct{ Thread struct{ ID string } }
			resumeErr := r.call("thread/resume", map[string]string{"threadId": nativeID}, &resumed)
			if mode == "new" {
				if resumeErr == nil || !strings.Contains(resumeErr.Error(), "no rollout found") {
					t.Fatalf("empty thread durability changed: %v %v", resumed, resumeErr)
				}
			} else if resumeErr != nil || resumed.Thread.ID != nativeID {
				t.Fatalf("native thread did not survive shutdown: mode=%s id=%s result=%v err=%v", mode, nativeID, resumed, resumeErr)
			}
			if statuses, _ := live.Status(ctx); len(statuses) != 0 {
				t.Fatal("closed terminal remained active", statuses)
			}
			endpoint := strings.TrimPrefix(spec.Args[1], "ws://")
			if conn, err := net.DialTimeout("tcp", endpoint, time.Second); err == nil {
				conn.Close()
				t.Fatal("relay remained listening after close")
			}
		})
	}
}
