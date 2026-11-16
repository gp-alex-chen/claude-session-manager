package provider

import (
	"context"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAntigravityACPHistoryHandshakePaginationAndWindowsPaths(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentCapabilities":{"sessionCapabilities":{"list":{}}}}}
{"jsonrpc":"2.0","id":2,"result":{"sessions":[{"sessionId":"01900000-0000-7000-8000-000000000001","cwd":"/D:/work/one","title":"Native title","updatedAt":"2026-01-01T01:02:03Z"}],"nextCursor":"second"}}
{"jsonrpc":"2.0","id":3,"result":{"sessions":[{"sessionId":"01900000-0000-7000-8000-000000000002","cwd":"C:\\work\\two","title":"","updatedAt":"2026-01-01T01:02:04Z"},{"sessionId":"unsafe&command","cwd":"C:/work"},{"sessionId":"01900000-0000-7000-8000-000000000003","cwd":"relative"}],"nextCursor":null}}
`
	f := &rpcFixture{Reader: strings.NewReader(body)}
	c := &AntigravityACP{Open: func(context.Context) (io.ReadWriteCloser, error) { return f, nil }}
	list, err := c.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Ref.Provider != Antigravity || list[0].Dir != filepath.Clean(`D:\work\one`) || list[0].Name != "Native title" || list[1].Name == "" {
		t.Fatalf("metadata: %#v", list)
	}
	var methods []string
	for _, r := range f.requests {
		methods = append(methods, r["method"].(string))
		if r["jsonrpc"] != "2.0" {
			t.Fatal("not ACP JSON-RPC 2.0", r)
		}
	}
	if !reflect.DeepEqual(methods, []string{"initialize", "session/list", "session/list"}) || !f.closed {
		t.Fatal(methods, f.closed)
	}
	if f.requests[2]["params"].(map[string]any)["cursor"] != "second" {
		t.Fatal("pagination omitted")
	}
}

func TestAntigravityACPRejectsUnavailableCapabilityAndIncompleteCatalog(t *testing.T) {
	init := `{"id":1,"result":{"protocolVersion":1,"agentCapabilities":{"sessionCapabilities":{"list":{}}}}}` + "\n"
	for _, body := range []string{
		`{"id":1,"result":{"protocolVersion":1,"agentCapabilities":{}}}` + "\n",
		`{"id":1,"result":{"protocolVersion":2,"agentCapabilities":{"sessionCapabilities":{"list":{}}}}}` + "\n",
		init + `{"id":2,"result":{}}` + "\n",
		init + `{"id":2,"result":{"sessions":[],"nextCursor":"same"}}` + "\n" + `{"id":3,"result":{"sessions":[],"nextCursor":"same"}}` + "\n",
		init + `{"id":2,"result":{"sessions":[],"nextCursor":"more"}}` + "\n" + `{"id":3,"error":{"message":"synthetic failure"}}` + "\n",
	} {
		f := &rpcFixture{Reader: strings.NewReader(body)}
		c := &AntigravityACP{Open: func(context.Context) (io.ReadWriteCloser, error) { return f, nil }}
		if list, err := c.ListSessions(context.Background()); err == nil || list != nil {
			t.Fatal("partial/unsupported catalog accepted", list, err)
		}
		if !f.closed {
			t.Fatal("ACP connection leaked")
		}
	}
}

func TestAntigravityWindowsDirectoryNormalization(t *testing.T) {
	for _, path := range []string{`relative`, `/home/example`, `C:relative`, "C:\\bad\npath", "C:\\work\t", `///D:/work`, `file:///D:/work`} {
		if _, err := normalizeAntigravityDirectory(path); err == nil {
			t.Fatalf("unsafe cwd accepted: %q", path)
		}
	}
	for _, path := range []string{`/D:/work/project/`, `D:\work\project`, `D:/work/project`} {
		got, err := normalizeAntigravityDirectory(path)
		if err != nil || got != filepath.Clean(`D:\work\project`) {
			t.Fatal(got, err)
		}
	}
}

func TestAntigravityACPPreservesIdentityWithoutOptionalDisplayTimestamp(t *testing.T) {
	body := `{"id":1,"result":{"protocolVersion":1,"agentCapabilities":{"sessionCapabilities":{"list":{}}}}}` + "\n" +
		`{"id":2,"result":{"sessions":[{"sessionId":"01900000-0000-7000-8000-000000000001","cwd":"C:/work"}]}}` + "\n"
	f := &rpcFixture{Reader: strings.NewReader(body)}
	c := &AntigravityACP{Open: func(context.Context) (io.ReadWriteCloser, error) { return f, nil }}
	list, err := c.ListSessions(context.Background())
	if err != nil || len(list) != 1 || !list[0].Time.IsZero() {
		t.Fatal("optional timestamp hid restorable identity", list, err)
	}
}

func TestAntigravityACPRejectsMalformedListCapabilityBeforeCallingList(t *testing.T) {
	for _, capability := range []string{`false`, `"invalid"`, `[]`} {
		body := `{"id":1,"result":{"protocolVersion":1,"agentCapabilities":{"sessionCapabilities":{"list":` + capability + `}}}}` + "\n" + `{"id":2,"result":{"sessions":[]}}` + "\n"
		f := &rpcFixture{Reader: strings.NewReader(body)}
		c := &AntigravityACP{Open: func(context.Context) (io.ReadWriteCloser, error) { return f, nil }}
		if _, err := c.ListSessions(context.Background()); err == nil || len(f.requests) != 1 {
			t.Fatalf("malformed capability %s used for list", capability)
		}
	}
}

func TestAntigravityACPLimitsAllRowsAndDetectsIdentityConflictsWithoutTimestamp(t *testing.T) {
	init := `{"id":1,"result":{"protocolVersion":1,"agentCapabilities":{"sessionCapabilities":{"list":{}}}}}` + "\n"
	for _, rows := range []string{
		strings.Repeat(`{"sessionId":"invalid"},`, 100000) + `{"sessionId":"invalid"}`,
		`{"sessionId":"01900000-0000-7000-8000-000000000001","cwd":"C:/one"},{"sessionId":"01900000-0000-7000-8000-000000000001","cwd":"C:/two"}`,
	} {
		body := init + `{"id":2,"result":{"sessions":[` + rows + `]}}` + "\n"
		f := &rpcFixture{Reader: strings.NewReader(body)}
		c := &AntigravityACP{Open: func(context.Context) (io.ReadWriteCloser, error) { return f, nil }}
		if list, err := c.ListSessions(context.Background()); err == nil || list != nil {
			t.Fatal("invalid complete catalog accepted", len(list), err)
		}
	}
}
