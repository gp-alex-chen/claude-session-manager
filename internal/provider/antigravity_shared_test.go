package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
)

type sharedACPFixture struct {
	mu            sync.Mutex
	opens, closes int
	methods       []string
	ids           []int
	block         chan struct{}
	entered       chan struct{}
}
type antigravitySharedFixtureConn struct {
	net.Conn
	once    sync.Once
	done    chan struct{}
	onClose func()
}

func (c *antigravitySharedFixtureConn) Close() error {
	c.once.Do(func() { close(c.done); c.Conn.Close(); c.onClose() })
	return nil
}
func (f *sharedACPFixture) open(ctx context.Context, bin string) (io.ReadWriteCloser, error) {
	client, server := net.Pipe()
	f.mu.Lock()
	f.opens++
	f.mu.Unlock()
	c := &antigravitySharedFixtureConn{Conn: client, done: make(chan struct{}), onClose: func() { f.mu.Lock(); f.closes++; f.mu.Unlock() }}
	go func() {
		defer server.Close()
		scanner := bufio.NewScanner(server)
		for scanner.Scan() {
			var req struct {
				ID     int
				Method string
			}
			if json.Unmarshal(scanner.Bytes(), &req) != nil {
				return
			}
			f.mu.Lock()
			f.methods = append(f.methods, req.Method)
			f.ids = append(f.ids, req.ID)
			block, entered := f.block, f.entered
			f.mu.Unlock()
			var result any = map[string]any{"sessions": []any{}}
			if req.Method == "initialize" {
				result = map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"sessionCapabilities": map[string]any{"list": map[string]any{}}}}
			}
			if req.Method == "session/list" && block != nil {
				select {
				case entered <- struct{}{}:
				default:
				}
				select {
				case <-block:
				case <-c.done:
					return
				case <-ctx.Done():
					return
				}
			}
			if json.NewEncoder(server).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}) != nil {
				return
			}
		}
	}()
	return c, nil
}
func (f *sharedACPFixture) counts() (int, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	initializes := 0
	for _, m := range f.methods {
		if m == "initialize" {
			initializes++
		}
	}
	return f.opens, f.closes, initializes
}

func TestSharedAntigravityACPReusesProcessAndHandshakeAcrossRequestContexts(t *testing.T) {
	f := &sharedACPFixture{}
	service := newSharedAntigravityACP(f.open)
	defer service.CloseService()
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		list, err := service.ListSessions(ctx, "fixture.exe")
		cancel()
		if err != nil || len(list) != 0 {
			t.Fatal(list, err)
		}
	}
	opens, closes, inits := f.counts()
	if opens != 1 || closes != 0 || inits != 1 {
		t.Fatalf("repeated cold start: opens=%d closes=%d initialize=%d", opens, closes, inits)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, id := range f.ids {
		if id != i+1 {
			t.Fatal("request ID reused", f.ids)
		}
	}
}

func TestSharedAntigravityACPCancelDiscardsConnectionAndNextQueryRebuilds(t *testing.T) {
	f := &sharedACPFixture{block: make(chan struct{}), entered: make(chan struct{}, 1)}
	service := newSharedAntigravityACP(f.open)
	defer service.CloseService()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := service.ListSessions(ctx, "fixture.exe"); done <- err }()
	<-f.entered
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled request succeeded")
	}
	f.mu.Lock()
	f.block = nil
	f.mu.Unlock()
	if _, err := service.ListSessions(context.Background(), "fixture.exe"); err != nil {
		t.Fatal(err)
	}
	opens, closes, inits := f.counts()
	if opens != 2 || closes != 1 || inits != 2 {
		t.Fatalf("dirty connection reused: %d/%d/%d", opens, closes, inits)
	}
}

func TestSharedAntigravityACPCloseAndRestartAreLifecycleScoped(t *testing.T) {
	f := &sharedACPFixture{}
	service := newSharedAntigravityACP(f.open)
	if _, err := service.ListSessions(context.Background(), "first.exe"); err != nil {
		t.Fatal(err)
	}
	service.CloseService()
	service.CloseService()
	if _, err := service.ListSessions(context.Background(), "first.exe"); err == nil {
		t.Fatal("closed service restarted itself")
	}
	service.BeginService()
	if _, err := service.ListSessions(context.Background(), "first.exe"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListSessions(context.Background(), "second.exe"); err != nil {
		t.Fatal(err)
	}
	service.CloseService()
	opens, closes, inits := f.counts()
	if opens != 3 || closes != 3 || inits != 3 {
		t.Fatal(opens, closes, inits)
	}
}

func TestAntigravityFreshHistoriesShareMetadataOwnerAndLifecycleInvalidatesCache(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "agy.exe")
	if err := os.WriteFile(bin, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	f := &sharedACPFixture{}
	p := NewAntigravity(AntigravityConfig{Lookup: func(string) (string, error) { return bin, nil }})
	p.metadata = newSharedAntigravityACP(f.open)
	defer p.CloseService()
	for i := 0; i < 3; i++ {
		if _, err := p.readHistory(context.Background(), true, bin); err != nil {
			t.Fatal(err)
		}
	}
	opens, _, inits := f.counts()
	if opens != 1 || inits != 1 {
		t.Fatal("adapter opened per-query services", opens, inits)
	}
	p.CloseService()
	if _, err := p.ListSessions(context.Background()); err == nil {
		t.Fatal("closed service returned a cached successful catalog")
	}
	p.BeginService()
	if _, err := p.ListSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	opens, _, inits = f.counts()
	if opens != 2 || inits != 2 {
		t.Fatal("new lifecycle reused old cached data", opens, inits)
	}
}

type antigravityBlockedWrite struct {
	io.ReadWriteCloser
	entered chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (c *antigravityBlockedWrite) Write([]byte) (int, error) {
	select {
	case c.entered <- struct{}{}:
	default:
	}
	<-c.closed
	return 0, io.ErrClosedPipe
}
func (c *antigravityBlockedWrite) Close() error {
	c.once.Do(func() { close(c.closed); c.ReadWriteCloser.Close() })
	return nil
}

func TestSharedAntigravityACPCancelUnblocksWriteAndReapsOldTransport(t *testing.T) {
	f := &sharedACPFixture{}
	entered := make(chan struct{}, 1)
	calls := 0
	service := newSharedAntigravityACP(func(ctx context.Context, bin string) (io.ReadWriteCloser, error) {
		calls++
		conn, err := f.open(ctx, bin)
		if err != nil {
			return nil, err
		}
		if calls == 1 {
			return &antigravityBlockedWrite{ReadWriteCloser: conn, entered: entered, closed: make(chan struct{})}, nil
		}
		return conn, nil
	})
	defer service.CloseService()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := service.ListSessions(ctx, "fixture.exe"); done <- err }()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("write cancellation did not return", err)
	}
	if _, err := service.ListSessions(context.Background(), "fixture.exe"); err != nil {
		t.Fatal(err)
	}
	opens, closes, inits := f.counts()
	if opens != 2 || closes != 1 || inits != 1 {
		t.Fatal("blocked writer survived or was reused", opens, closes, inits)
	}
}

func TestSharedAntigravityACPQueuedCancellationDoesNotAbortCurrentQuery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &sharedACPFixture{block: make(chan struct{}), entered: make(chan struct{}, 1)}
		service := newSharedAntigravityACP(f.open)
		defer service.CloseService()
		first := make(chan error, 1)
		go func() { _, err := service.ListSessions(context.Background(), "fixture.exe"); first <- err }()
		<-f.entered
		ctx, cancel := context.WithCancel(context.Background())
		second := make(chan error, 1)
		go func() { _, err := service.ListSessions(ctx, "fixture.exe"); second <- err }()
		synctest.Wait()
		cancel()
		if err := <-second; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		opens, closes, _ := f.counts()
		if opens != 1 || closes != 0 {
			t.Fatal("waiting caller closed the active connection", opens, closes)
		}
		close(f.block)
		if err := <-first; err != nil {
			t.Fatal("healthy active query was canceled", err)
		}
	})
}

func TestSharedAntigravityACPTimeoutRebuildsWithoutRealWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &sharedACPFixture{block: make(chan struct{}), entered: make(chan struct{}, 1)}
		service := newSharedAntigravityACP(f.open)
		defer service.CloseService()
		list, err := service.ListSessions(context.Background(), "fixture.exe")
		if !errors.Is(err, context.DeadlineExceeded) || list != nil {
			t.Fatal("timed out query returned a catalog", list, err)
		}
		f.mu.Lock()
		f.block = nil
		f.mu.Unlock()
		if _, err = service.ListSessions(context.Background(), "fixture.exe"); err != nil {
			t.Fatal(err)
		}
		opens, closes, _ := f.counts()
		if opens != 2 || closes != 1 {
			t.Fatal(opens, closes)
		}
	})
}

func TestSharedAntigravityACPLateOpenCannotPublishAfterCloseAndBegin(t *testing.T) {
	f := &sharedACPFixture{}
	entered, release := make(chan struct{}), make(chan struct{})
	calls := 0
	service := newSharedAntigravityACP(func(ctx context.Context, bin string) (io.ReadWriteCloser, error) {
		calls++
		if calls == 1 {
			close(entered)
			<-release
		}
		return f.open(ctx, bin)
	})
	defer service.CloseService()
	old := make(chan error, 1)
	go func() { _, err := service.ListSessions(context.Background(), "fixture.exe"); old <- err }()
	<-entered
	service.CloseService()
	service.BeginService()
	fresh := make(chan error, 1)
	go func() { _, err := service.ListSessions(context.Background(), "fixture.exe"); fresh <- err }()
	close(release)
	if err := <-old; err == nil {
		t.Fatal("old open published to a new epoch")
	}
	if err := <-fresh; err != nil {
		t.Fatal("late cleanup damaged the new service", err)
	}
	if _, err := service.ListSessions(context.Background(), "fixture.exe"); err != nil {
		t.Fatal(err)
	}
	opens, closes, inits := f.counts()
	if opens != 2 || closes != 1 || inits != 1 {
		t.Fatal(opens, closes, inits)
	}
}

func TestSharedAntigravityACPPartialFailureDiscardsWholeConnection(t *testing.T) {
	init := `{"id":1,"result":{"protocolVersion":1,"agentCapabilities":{"sessionCapabilities":{"list":{}}}}}` + "\n"
	failed := &rpcFixture{Reader: strings.NewReader(init + `{"id":2,"result":{"sessions":[{"sessionId":"01900000-0000-7000-8000-000000000001","cwd":"C:/synthetic"}],"nextCursor":"more"}}` + "\n" + `{"id":3,"error":{"message":"synthetic failure"}}` + "\n")}
	next := &rpcFixture{Reader: strings.NewReader(init + `{"id":2,"result":{"sessions":[]}}` + "\n")}
	opens := 0
	service := newSharedAntigravityACP(func(context.Context, string) (io.ReadWriteCloser, error) {
		opens++
		if opens == 1 {
			return failed, nil
		}
		return next, nil
	})
	defer service.CloseService()
	if list, err := service.ListSessions(context.Background(), "fixture.exe"); err == nil || list != nil || !failed.closed {
		t.Fatal("partial catalog was accepted", list, err, failed.closed)
	}
	if _, err := service.ListSessions(context.Background(), "fixture.exe"); err != nil || opens != 2 {
		t.Fatal("failed connection was reused", opens, err)
	}
}

type antigravityDelayedClose struct {
	io.ReadWriteCloser
	entered, release chan struct{}
	once             sync.Once
}

func (c *antigravityDelayedClose) Close() error {
	c.once.Do(func() { close(c.entered); <-c.release; c.ReadWriteCloser.Close() })
	return nil
}

func TestSharedAntigravityACPCloseWaitsForAlreadyDetachedConnections(t *testing.T) {
	for _, mode := range []string{"binary-change", "discard"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := &sharedACPFixture{}
				entered, release := make(chan struct{}), make(chan struct{})
				opens := 0
				service := newSharedAntigravityACP(func(ctx context.Context, bin string) (io.ReadWriteCloser, error) {
					opens++
					conn, err := f.open(ctx, bin)
					if opens == 1 {
						return &antigravityDelayedClose{ReadWriteCloser: conn, entered: entered, release: release}, err
					}
					return conn, err
				})
				defer service.CloseService()
				if _, err := service.ListSessions(context.Background(), "first.exe"); err != nil {
					t.Fatal(err)
				}
				actionDone := make(chan struct{})
				go func() {
					defer close(actionDone)
					if mode == "binary-change" {
						service.ListSessions(context.Background(), "second.exe")
					} else {
						service.mu.Lock()
						entry := service.current
						service.mu.Unlock()
						service.discard(entry)
					}
				}()
				<-entered
				closed := make(chan struct{})
				go func() { service.CloseService(); close(closed) }()
				synctest.Wait()
				early := false
				select {
				case <-closed:
					early = true
				default:
				}
				close(release)
				<-actionDone
				<-closed
				if early {
					t.Fatal("CloseService returned while a detached ACP was still closing")
				}
				service.BeginService()
				if _, err := service.ListSessions(context.Background(), "second.exe"); err != nil {
					t.Fatal("old retirement damaged new connection", err)
				}
			})
		})
	}
}

func TestSharedAntigravityACPDrainsIdleNotificationsWithoutBlockingNextWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		firstNotification := make(chan struct{})
		service := newSharedAntigravityACP(func(context.Context, string) (io.ReadWriteCloser, error) {
			client, server := net.Pipe()
			go func() {
				defer server.Close()
				scanner := bufio.NewScanner(server)
				lists := 0
				for scanner.Scan() {
					var req struct {
						ID     int
						Method string
					}
					if json.Unmarshal(scanner.Bytes(), &req) != nil {
						return
					}
					var result any = map[string]any{"sessions": []any{}}
					if req.Method == "initialize" {
						result = map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"sessionCapabilities": map[string]any{"list": map[string]any{}}}}
					} else {
						lists++
					}
					enc := json.NewEncoder(server)
					if enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}) != nil {
						return
					}
					if req.Method == "session/list" && lists == 1 {
						for i := 0; i < 3; i++ {
							if enc.Encode(map[string]any{"jsonrpc": "2.0", "method": "metadata/status", "params": map[string]any{}}) != nil {
								return
							}
							if i == 0 {
								close(firstNotification)
							}
						}
					}
				}
			}()
			return client, nil
		})
		defer service.CloseService()
		if _, err := service.ListSessions(context.Background(), "fixture.exe"); err != nil {
			t.Fatal(err)
		}
		<-firstNotification
		synctest.Wait()
		if _, err := service.ListSessions(context.Background(), "fixture.exe"); err != nil {
			t.Fatal("idle notifications blocked the next request", err)
		}
	})
}
