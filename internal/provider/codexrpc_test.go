package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

type rpcFixture struct {
	*strings.Reader
	requests []map[string]any
	closed   bool
}

func (f *rpcFixture) Write(p []byte) (int, error) {
	var m map[string]any
	if err := json.Unmarshal(p, &m); err != nil {
		return 0, err
	}
	f.requests = append(f.requests, m)
	return len(p), nil
}
func (f *rpcFixture) Close() error { f.closed = true; return nil }

func TestCodexHistoryHandshakePaginationAndMetadata(t *testing.T) {
	response := `{"id":1,"result":{}}
{"method":"thread/started","params":{}}
{"id":2,"result":{"data":[{"id":"01900000-0000-7000-8000-000000000001","cwd":"C:\\work","name":"Existing title","updatedAt":12}],"nextCursor":"page-2"}}
{"id":3,"result":{"data":[{"id":"01900000-0000-7000-8000-000000000002","cwd":"C:\\other","preview":"First prompt","updatedAt":13},{"id":"unsafe&command","cwd":"C:\\work"}],"nextCursor":null}}
`
	f := &rpcFixture{Reader: strings.NewReader(response)}
	c := &CodexRPC{Open: func(context.Context) (io.ReadWriteCloser, error) { return f, nil }}
	list, err := c.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "Existing title" || list[1].Name != "First prompt" || list[0].Ref.Provider != Codex {
		t.Fatalf("history: %#v", list)
	}
	var methods []string
	for _, m := range f.requests {
		methods = append(methods, m["method"].(string))
	}
	if !reflect.DeepEqual(methods, []string{"initialize", "initialized", "thread/list", "thread/list"}) {
		t.Fatalf("requests: %v", methods)
	}
	if f.requests[3]["params"].(map[string]any)["cursor"] != "page-2" || !f.closed {
		t.Fatalf("cursor/close: %#v", f)
	}
}

func TestCodexHistoryRejectsProtocolFailureAndPartialHistory(t *testing.T) {
	for _, body := range []string{
		`not-json`,
		`{"id":1,"error":{"code":-32600,"message":"unsupported client"}}`,
		"{\"id\":1,\"result\":{}}\n{\"id\":2,\"error\":{\"message\":\"cannot list\"}}\n",
		"{\"id\":1,\"result\":{}}\n{\"id\":99,\"result\":{}}\n",
		"{\"id\":1,\"result\":{}}\n{\"id\":2,\"result\":{}}\n",
		"{\"id\":1,\"result\":{}}\n{\"id\":2,\"result\":{\"data\":null}}\n",
	} {
		f := &rpcFixture{Reader: strings.NewReader(body)}
		c := &CodexRPC{Open: func(context.Context) (io.ReadWriteCloser, error) { return f, nil }}
		if _, err := c.ListSessions(context.Background()); err == nil {
			t.Fatalf("accepted %q", body)
		}
		if !f.closed {
			t.Fatal("transport not closed")
		}
	}
}

type blockedRPC struct {
	reader  *io.PipeReader
	writer  *io.PipeWriter
	request chan struct{}
}

func (f *blockedRPC) Read(p []byte) (int, error) { return f.reader.Read(p) }
func (f *blockedRPC) Write(p []byte) (int, error) {
	f.request <- struct{}{}
	return len(p), nil
}
func (f *blockedRPC) Close() error {
	_ = f.writer.Close()
	return f.reader.Close()
}

func TestCodexHistoryCancellationClosesBlockedTransport(t *testing.T) {
	reader, writer := io.Pipe()
	f := &blockedRPC{reader: reader, writer: writer, request: make(chan struct{}, 1)}
	c := &CodexRPC{Open: func(context.Context) (io.ReadWriteCloser, error) { return f, nil }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.ListSessions(ctx); done <- err }()
	<-f.request
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if _, err := writer.Write([]byte("ignored")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("transport remained open: %v", err)
	}
}

func TestCodexHistoryDoesNotAcceptRepeatedPaginationCursor(t *testing.T) {
	f := &rpcFixture{Reader: strings.NewReader("{\"id\":1,\"result\":{}}\n{\"id\":2,\"result\":{\"data\":[],\"nextCursor\":\"same\"}}\n{\"id\":3,\"result\":{\"data\":[],\"nextCursor\":\"same\"}}\n")}
	c := &CodexRPC{Open: func(context.Context) (io.ReadWriteCloser, error) { return f, nil }}
	if _, err := c.ListSessions(context.Background()); err == nil {
		t.Fatal("accepted pagination loop")
	}
}
