package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type trackedCancellation struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *trackedCancellation) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func TestRelayCancellationWhileWaitingForWriteGateNeverSends(t *testing.T) {
	r, f := testRelay(t)
	dialRelay(t, r)
	if err := r.acquireWrite(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer r.releaseWrite()
	parent, cancel := context.WithCancel(context.Background())
	ctx := &trackedCancellation{Context: parent, entered: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- r.call(ctx, "turn/start", nil, nil, nil) }()
	<-ctx.entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case request := <-f.requests:
		t.Fatal("cancelled request sent:", request)
	default:
	}
}

type blockedRelayWrite struct {
	io.ReadWriteCloser
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
}

type delayedRelayClose struct {
	*blockedRelayWrite
	allow chan struct{}
}

func (w *delayedRelayClose) Close() error { <-w.allow; return w.blockedRelayWrite.Close() }

func TestRelayCancelledWritePreventsNewWritersBeforeCloseFinishes(t *testing.T) {
	reader, output := io.Pipe()
	f := &relayFixture{PipeReader: reader, output: output, requests: make(chan string, 4), closed: make(chan struct{})}
	writer := &delayedRelayClose{blockedRelayWrite: &blockedRelayWrite{ReadWriteCloser: f, started: make(chan struct{}), closed: make(chan struct{})}, allow: make(chan struct{})}
	r, err := newCodexRelay(context.Background(), writer, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	defer close(writer.allow)
	dialRelay(t, r)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.call(ctx, "turn/start", nil, nil, nil) }()
	<-writer.started
	cancel()
	<-done
	if err := r.acquireWrite(context.Background()); err == nil {
		r.releaseWrite()
		t.Fatal("new write entered before prior cancelled write stopped")
	}
}

func (w *blockedRelayWrite) Write([]byte) (int, error) {
	close(w.started)
	<-w.closed
	return 0, io.ErrClosedPipe
}
func (w *blockedRelayWrite) Close() error {
	w.once.Do(func() { close(w.closed); w.ReadWriteCloser.Close() })
	return nil
}

func TestRelayCancellationInsideWriteClosesTransportAndUnblocks(t *testing.T) {
	reader, output := io.Pipe()
	f := &relayFixture{PipeReader: reader, output: output, requests: make(chan string, 4), closed: make(chan struct{})}
	writer := &blockedRelayWrite{ReadWriteCloser: f, started: make(chan struct{}), closed: make(chan struct{})}
	r, err := newCodexRelay(context.Background(), writer, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	dialRelay(t, r)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.call(ctx, "turn/start", nil, nil, nil) }()
	<-writer.started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked write ignored cancellation")
	}
	<-writer.closed
}

func TestRelayInternalRPCKeepsNativeMessagesAndRoutesOnlyResponses(t *testing.T) {
	r, f := testRelay(t)
	ws := dialRelay(t, r)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		var result struct{ OK bool }
		err := r.call(ctx, "thread/read", map[string]any{"threadId": telemetryID}, &result, nil)
		if err == nil && !result.OK {
			err = errors.New("missing result")
		}
		done <- err
	}()
	var request map[string]json.RawMessage
	if err := json.Unmarshal([]byte(<-f.requests), &request); err != nil {
		t.Fatal(err)
	}
	internalID := string(request["id"])
	// An approval request with the same ID must still reach the TUI.
	approval := `{"id":` + internalID + `,"method":"item/commandExecution/requestApproval","params":{}}`
	go f.output.Write([]byte(approval + "\n"))
	_, data, err := ws.ReadMessage()
	if err != nil || string(data) != approval {
		t.Fatalf("approval swallowed: %s %v", data, err)
	}
	// Even an unusual native request that collides with the private namespace works.
	native := `{"id":` + internalID + `,"method":"thread/read","params":{}}`
	if err := ws.WriteMessage(websocket.TextMessage, []byte(native)); err != nil {
		t.Fatal(err)
	}
	var forwarded map[string]json.RawMessage
	json.Unmarshal([]byte(<-f.requests), &forwarded)
	if string(forwarded["id"]) == internalID {
		t.Fatal("native/internal ID collision")
	}
	go f.output.Write([]byte(`{"id":` + internalID + `,"result":{"ok":true}}` + "\n" + `{"id":` + string(forwarded["id"]) + `,"result":{"native":true}}` + "\n"))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_, data, err = ws.ReadMessage()
	var reply map[string]json.RawMessage
	json.Unmarshal(data, &reply)
	if err != nil || string(reply["id"]) != internalID || !strings.Contains(string(reply["result"]), "native") {
		t.Fatalf("wrong native reply: %s %v", data, err)
	}
}

func TestRelayInternalRPCCancellationAndGuardDoNotWriteOrLeak(t *testing.T) {
	r, _ := testRelay(t)
	_ = dialRelay(t, r)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.call(ctx, "thread/read", nil, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	want := errors.New("busy")
	if err := r.call(context.Background(), "turn/start", nil, nil, func() error { return want }); !errors.Is(err, want) {
		t.Fatal(err)
	}
	r.Close()
	if err := r.call(context.Background(), "thread/read", nil, nil, nil); err == nil {
		t.Fatal("closed transport accepted RPC")
	}
}
