package provider

import (
	"context"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type sharedFixtureConn struct {
	*strings.Reader
	closed *atomic.Int32
}

func (c *sharedFixtureConn) Write(data []byte) (int, error) { return len(data), nil }
func (c *sharedFixtureConn) Close() error                   { c.closed.Add(1); return nil }

func fakeSharedInstance(generation uint64, stopped, connections *atomic.Int32) *sharedCodexInstance {
	done := make(chan struct{})
	var once sync.Once
	return &sharedCodexInstance{generation: generation, done: done, close: func() { once.Do(func() { stopped.Add(1); close(done) }) }, connect: func(context.Context) (io.ReadWriteCloser, error) {
		return &sharedFixtureConn{Reader: strings.NewReader(""), closed: connections}, nil
	}}
}

func TestSharedCodexStartsOnceAndClosingConnectionsDoesNotStopService(t *testing.T) {
	var starts, stops, connections atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	owner := newSharedCodexOwner(func(ctx context.Context, generation uint64) (*sharedCodexInstance, error) {
		starts.Add(1)
		close(entered)
		select {
		case <-release:
			return fakeSharedInstance(generation, &stops, &connections), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	defer owner.Close()
	results := make(chan io.ReadWriteCloser, 8)
	errs := make(chan error, 8)
	for range 8 {
		go func() {
			_, conn, err := owner.connect(context.Background())
			if err != nil {
				errs <- err
				return
			}
			results <- conn
		}()
	}
	<-entered
	close(release)
	for range 8 {
		select {
		case conn := <-results:
			conn.Close()
		case err := <-errs:
			t.Fatal(err)
		}
	}
	if starts.Load() != 1 || stops.Load() != 0 || connections.Load() != 8 {
		t.Fatal(starts.Load(), stops.Load(), connections.Load())
	}
	owner.Close()
	if stops.Load() != 1 {
		t.Fatal("service did not close exactly once")
	}
}

func TestSharedCodexWaiterCancellationDoesNotCancelOtherLaunches(t *testing.T) {
	var stops, connections atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	owner := newSharedCodexOwner(func(ctx context.Context, generation uint64) (*sharedCodexInstance, error) {
		close(entered)
		select {
		case <-release:
			return fakeSharedInstance(generation, &stops, &connections), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	defer owner.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan error, 1)
	go func() { _, _, err := owner.connect(ctx); cancelled <- err }()
	<-entered
	cancel()
	if err := <-cancelled; err != context.Canceled {
		t.Fatal(err)
	}
	close(release)
	_, conn, err := owner.connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if stops.Load() != 0 {
		t.Fatal("one waiter cancelled shared service")
	}
}

func TestSharedCodexSealCrashAndNextLifecycle(t *testing.T) {
	var starts, stops, connections atomic.Int32
	owner := newSharedCodexOwner(func(_ context.Context, generation uint64) (*sharedCodexInstance, error) {
		starts.Add(1)
		return fakeSharedInstance(generation, &stops, &connections), nil
	})
	defer owner.Close()
	first, conn, err := owner.connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	first.close()
	second, conn, err := owner.connect(context.Background())
	if err != nil || second.generation == first.generation {
		t.Fatal(second, err)
	}
	conn.Close()
	owner.Seal()
	_, conn, err = owner.connect(context.Background())
	if err != nil {
		t.Fatal("sealed service could not read final metadata", err)
	}
	conn.Close()
	second.close()
	if _, _, err := owner.connect(context.Background()); err == nil {
		t.Fatal("shutdown restarted service")
	}
	owner.Close()
	if _, _, err := owner.connect(context.Background()); err == nil {
		t.Fatal("closed service restarted")
	}
	owner.Begin()
	third, conn, err := owner.connect(context.Background())
	if err != nil || third.generation <= second.generation {
		t.Fatal(third, err)
	}
	conn.Close()
	if starts.Load() != 3 {
		t.Fatal(starts.Load())
	}
}

func TestSharedCodexDoesNotReturnRetiredConnectionWhileCloseWaits(t *testing.T) {
	dialed, allowDial, closing, allowClose := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var connections atomic.Int32
	done := make(chan struct{})
	owner := newSharedCodexOwner(func(context.Context, uint64) (*sharedCodexInstance, error) {
		return &sharedCodexInstance{done: done, connect: func(context.Context) (io.ReadWriteCloser, error) {
			close(dialed)
			<-allowDial
			return &sharedFixtureConn{Reader: strings.NewReader(""), closed: &connections}, nil
		}, close: func() { close(closing); <-allowClose; close(done) }}, nil
	})
	result := make(chan error, 1)
	go func() { _, _, err := owner.connect(context.Background()); result <- err }()
	<-dialed
	closed := make(chan struct{})
	go func() { owner.Close(); close(closed) }()
	<-closing
	close(allowDial)
	if err := <-result; err == nil {
		t.Error("retired connection returned while process still alive")
	}
	if connections.Load() != 1 {
		t.Error("retired connection not closed")
	}
	close(allowClose)
	<-closed
}

func TestSharedAddressWaitsForCompleteStartupLine(t *testing.T) {
	w := &sharedAddressWriter{ready: make(chan string, 1)}
	w.Write([]byte("codex app-server (WebSockets)\n  listening on: ws://127.0.0.1:49"))
	select {
	case got := <-w.ready:
		t.Fatalf("incomplete port published: %s", got)
	default:
	}
	w.Write([]byte("869\r\n  readyz: http://127.0.0.1:49869/readyz\n"))
	select {
	case got := <-w.ready:
		if got != "ws://127.0.0.1:49869" {
			t.Fatal(got)
		}
	default:
		t.Fatal("complete startup line was not detected")
	}
}
