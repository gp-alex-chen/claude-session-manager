package provider

import (
	"context"
	"io"
	"os"
	"os/exec"
)

// NewSharedCodexLive owns one lazily started native app-server. Every TUI and
// metadata request has its own authenticated upstream connection.
func NewSharedCodexLive(lookup func(string) (string, error), changed func(), stores ...NativeToolState) *CodexLive {
	return newSharedCodexLive(lookup, nil, "", changed, stores...)
}

func (p *CodexLive) restoreNativeToolState(stores []NativeToolState) {
	if len(stores) > 0 && stores[0] != nil {
		p.nativeToolState = stores[0]
		values, err := stores[0].LoadCodexNativeTools()
		if err == nil {
			p.nativeCatalogs = values
			p.persistedCatalogs = map[string]bool{}
			for id, catalog := range values {
				p.persistedCatalogs[id] = catalog == nativeReferenceCatalog
			}
		}
	}
}

func newSharedCodexLive(lookup func(string) (string, error), env []string, root string, changed func(), stores ...NativeToolState) *CodexLive {
	if lookup == nil {
		lookup = exec.LookPath
	}
	p := NewCodexLive(nil, nil, changed)
	p.nativeTools = true
	p.restoreNativeToolState(stores)
	p.shared = newSharedCodexOwner(func(ctx context.Context, generation uint64) (*sharedCodexInstance, error) {
		bin, err := ResolveCodexExecutable(lookup)
		if err != nil {
			return nil, err
		}
		dir := root
		if dir == "" {
			dir, err = os.UserHomeDir()
			if err != nil {
				return nil, err
			}
		}
		return startSharedCodexProcess(ctx, bin, dir, env, nil, generation, nil)
	})
	p.CodexAdapter = NewCodex(&CodexRPC{Open: func(ctx context.Context) (io.ReadWriteCloser, error) {
		_, conn, err := p.shared.connect(ctx)
		return conn, err
	}})
	p.open = func(ctx context.Context, dir string, client, server func([]byte), disconnect func()) (LaunchSpec, error) {
		instance, conn, err := p.shared.connect(ctx)
		if err != nil {
			return LaunchSpec{}, err
		}
		// Wails may cancel its context before the final persistence snapshot.
		// The terminal owns this relay; the application owns the server.
		relay, err := newCodexRelay(context.Background(), conn, client, server, disconnect)
		if err != nil {
			conn.Close()
			return LaunchSpec{}, err
		}
		if err := ctx.Err(); err != nil {
			relay.Close()
			return LaunchSpec{}, err
		}
		// ConPTY and the native registration hook both carry the project cwd.
		// The path never enters a shell command's quoting/expansion layer.
		return LaunchSpec{Command: "codex", Executable: instance.executable, Args: []string{"--remote", relay.url, "--remote-auth-token-env", codexTokenEnv}, Env: append([]string{codexTokenEnv + "=" + relay.token}, codexExecutableEnvironment(instance.executable, env)...), Close: relay.Close, rpc: relay, shared: instance}, nil
	}
	return p
}

func (p *CodexLive) BeginService() {
	if p.shared != nil {
		p.shared.Begin()
	}
}
func (p *CodexLive) CloseService() {
	if p.shared != nil {
		p.shared.Close()
	}
}
