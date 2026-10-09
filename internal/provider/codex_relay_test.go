package provider

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type relayFixture struct {
	*io.PipeReader
	output   *io.PipeWriter
	requests chan string
	closed   chan struct{}
	once     sync.Once
}

func (f *relayFixture) Write(data []byte) (int, error) {
	f.requests <- string(data)
	return len(data), nil
}
func (f *relayFixture) Close() error {
	f.once.Do(func() { f.PipeReader.Close(); f.output.Close(); close(f.closed) })
	return nil
}
func testRelay(t *testing.T) (*codexRelay, *relayFixture) {
	t.Helper()
	reader, writer := io.Pipe()
	f := &relayFixture{PipeReader: reader, output: writer, requests: make(chan string, 4), closed: make(chan struct{})}
	r, err := newCodexRelay(context.Background(), f, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r, f
}
func dialRelay(t *testing.T, r *codexRelay) *websocket.Conn {
	t.Helper()
	ws, _, err := websocket.DefaultDialer.Dial(r.url, http.Header{"Authorization": []string{"Bearer " + r.token}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	return ws
}
func TestCodexRelayAuthenticatesBeforeReservingSingleClient(t *testing.T) {
	r, _ := testRelay(t)
	for _, header := range []http.Header{nil, {"Authorization": []string{"Bearer wrong"}}, {"Authorization": []string{"Bearer " + r.token}, "Origin": []string{"https://example.invalid"}}} {
		ws, res, err := websocket.DefaultDialer.Dial(r.url, header)
		if ws != nil {
			ws.Close()
		}
		if err == nil || res == nil || res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauthorized accepted: %v %#v", err, res)
		}
		res.Body.Close()
	}
	_ = dialRelay(t, r)
	ws, res, err := websocket.DefaultDialer.Dial(r.url, http.Header{"Authorization": []string{"Bearer " + r.token}})
	if ws != nil {
		ws.Close()
	}
	if err == nil || res == nil || res.StatusCode != http.StatusConflict {
		t.Fatalf("second client accepted: %v %#v", err, res)
	}
	res.Body.Close()
}
func TestCodexRelayPreservesUnknownFieldsAndApprovalMessagesBothDirections(t *testing.T) {
	r, f := testRelay(t)
	ws := dialRelay(t, r)
	client := `{"id":"opaque-string","method":"future/newMethod","params":{"escaped":"a\\b","unknown":42}}`
	if err := ws.WriteMessage(websocket.TextMessage, []byte(client)); err != nil {
		t.Fatal(err)
	}
	if got := <-f.requests; got != client+"\n" {
		t.Fatalf("rewritten request: %q", got)
	}
	approval := `{"id":"approval-id","method":"item/commandExecution/requestApproval","params":{"threadId":"` + telemetryID + `","unknown":true}}`
	result := `{"id":123,"result":{"future":null}}`
	done := make(chan error, 1)
	go func() {
		_, err := f.output.Write([]byte(approval[:15]))
		if err == nil {
			_, err = f.output.Write([]byte(approval[15:] + "\r\n" + result + "\n"))
		}
		done <- err
	}()
	for _, expected := range []string{approval, result} {
		_, got, err := ws.ReadMessage()
		if err != nil || string(got) != expected {
			t.Fatalf("rewritten server payload: %q %v", got, err)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	response := `{"id":"approval-id","result":{"decision":"decline"}}`
	if err := ws.WriteMessage(websocket.TextMessage, []byte(response)); err != nil {
		t.Fatal(err)
	}
	if got := <-f.requests; got != response+"\n" {
		t.Fatalf("approval response changed: %q", got)
	}
	ws.Close()
	<-f.closed
	r.Close()
}
func TestCodexRelayMalformedObservationDoesNotBlockTUI(t *testing.T) {
	r, f := testRelay(t)
	live := NewCodexLive(NewCodex(nil), nil, nil)
	o := live.observe("new-codex-relay", `C:\work`)
	r.clientObserver = o.client
	r.serverObserver = o.server
	ws := dialRelay(t, r)
	message := `{"method":"future/event","params":{"newField":"unterpreted"}}`
	go f.output.Write([]byte(message + "\n"))
	_, got, err := ws.ReadMessage()
	if err != nil || strings.TrimSpace(string(got)) != message {
		t.Fatalf("observer affected forward: %q %v", got, err)
	}
}
