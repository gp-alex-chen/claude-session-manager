package provider

import (
	"context"
	"errors"
	"io"
	"sync"
)

type sharedCodexInstance struct {
	generation uint64
	pid        int
	executable string
	url, token string
	done       <-chan struct{}
	connect    func(context.Context) (io.ReadWriteCloser, error)
	close      func()
}

func (s *sharedCodexInstance) alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

type sharedCodexStart struct {
	done   chan struct{}
	cancel context.CancelFunc
	err    error
}

// A service belongs to one TheOne lifecycle, never to a PTY or a metadata
// request. Its start context is independent of individual waiting clients.
type sharedCodexOwner struct {
	mu             sync.Mutex
	start          func(context.Context, uint64) (*sharedCodexInstance, error)
	current        *sharedCodexInstance
	pending        *sharedCodexStart
	ctx            context.Context
	cancel         context.CancelFunc
	sequence       uint64
	closed, sealed bool
}

func newSharedCodexOwner(start func(context.Context, uint64) (*sharedCodexInstance, error)) *sharedCodexOwner {
	o := &sharedCodexOwner{start: start, closed: true}
	o.Begin()
	return o
}
func (o *sharedCodexOwner) Begin() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.closed {
		return
	}
	o.ctx, o.cancel = context.WithCancel(context.Background())
	o.closed = false
	o.sealed = false
}
func (o *sharedCodexOwner) Seal() {
	o.mu.Lock()
	o.sealed = true
	pending := o.pending
	o.mu.Unlock()
	if pending != nil {
		pending.cancel()
	}
}
func (o *sharedCodexOwner) Close() {
	o.mu.Lock()
	o.closed = true
	o.sealed = true
	cancel := o.cancel
	pending, current := o.pending, o.current
	o.pending = nil
	o.current = nil
	o.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if pending != nil {
		pending.cancel()
		<-pending.done
	}
	if current != nil {
		current.close()
	}
}
func (o *sharedCodexOwner) connect(ctx context.Context) (*sharedCodexInstance, io.ReadWriteCloser, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		o.mu.Lock()
		if o.closed {
			o.mu.Unlock()
			return nil, nil, errors.New("共享 Codex 服务已关闭")
		}
		if o.current != nil && o.current.alive() {
			instance := o.current
			o.mu.Unlock()
			conn, err := instance.connect(ctx)
			if err != nil {
				return nil, nil, err
			}
			o.mu.Lock()
			valid := !o.closed && o.current == instance && instance.alive()
			o.mu.Unlock()
			if !valid {
				conn.Close()
				return nil, nil, errors.New("Codex 服务已退出，请重新打开终端")
			}
			return instance, conn, nil
		}
		if o.sealed {
			o.mu.Unlock()
			return nil, nil, errors.New("应用正在退出，不再启动 Codex 服务")
		}
		pending := o.pending
		if pending == nil {
			root, cancel := context.WithCancel(o.ctx)
			pending = &sharedCodexStart{done: make(chan struct{}), cancel: cancel}
			o.pending = pending
			o.sequence++
			generation := o.sequence
			go o.launch(root, pending, generation)
		}
		o.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-pending.done:
			if pending.err != nil {
				return nil, nil, pending.err
			}
		}
	}
}
func (o *sharedCodexOwner) launch(ctx context.Context, pending *sharedCodexStart, generation uint64) {
	instance, err := o.start(ctx, generation)
	o.mu.Lock()
	install := err == nil && !o.closed && !o.sealed && o.pending == pending
	if install {
		o.current = instance
	} else if err == nil {
		err = errors.New("Codex 服务启动已取消")
	}
	if o.pending == pending {
		o.pending = nil
	}
	pending.err = err
	o.mu.Unlock()
	if !install && instance != nil {
		instance.close()
	}
	close(pending.done)
}
