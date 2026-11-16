package provider

import (
	"context"
	"errors"
	"io"
	"sync"
)

var errAntigravityACPStopped = errors.New("Antigravity metadata service is stopped")

type antigravityACPConnection struct {
	bin         string
	conn        io.ReadWriteCloser
	rpc         *rpcSession
	cancel      context.CancelFunc
	once        sync.Once
	initialized bool // query gate owns this flag and the RPC request counter.
}

func (c *antigravityACPConnection) close(abort bool) {
	c.once.Do(func() {
		if fast, ok := c.conn.(interface{ Abort() error }); abort && ok {
			_ = fast.Abort()
		} else {
			_ = c.conn.Close()
		}
		c.cancel()
	})
}

// Abort is used only for a dirty/cancelled metadata transport. Normal Close
// still allows the native helper its bounded graceful shutdown interval.
func (p *processConnection) Abort() error {
	p.once.Do(func() {
		_ = p.cmd.Process.Kill()
		_ = p.Writer.(io.Closer).Close()
		_ = p.Reader.(io.Closer).Close()
		_ = p.cmd.Wait()
	})
	return nil
}

type antigravityACPOpenAttempt struct{ cancel context.CancelFunc }
type sharedAntigravityACP struct {
	mu       sync.Mutex
	gate     chan struct{}
	open     func(context.Context, string) (io.ReadWriteCloser, error)
	enabled  bool
	epoch    uint64
	done     chan struct{}
	current  *antigravityACPConnection
	opening  *antigravityACPOpenAttempt
	retiring map[*antigravityACPConnection]chan struct{}
}

func newSharedAntigravityACP(open func(context.Context, string) (io.ReadWriteCloser, error)) *sharedAntigravityACP {
	return &sharedAntigravityACP{gate: make(chan struct{}, 1), open: open, enabled: true, epoch: 1, done: make(chan struct{})}
}
func (s *sharedAntigravityACP) state() (uint64, <-chan struct{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled {
		return s.epoch, s.done, errAntigravityACPStopped
	}
	return s.epoch, s.done, nil
}
func (s *sharedAntigravityACP) BeginService() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.enabled {
		return
	}
	s.enabled = true
	s.epoch++
	s.done = make(chan struct{})
}

// Every detach registers its retirement before releasing mu. App shutdown
// must also wait for connections already being discarded or replaced.
func (s *sharedAntigravityACP) retireLocked(entry *antigravityACPConnection) (chan struct{}, bool) {
	if entry == nil {
		return nil, false
	}
	if s.retiring == nil {
		s.retiring = map[*antigravityACPConnection]chan struct{}{}
	}
	if done := s.retiring[entry]; done != nil {
		return done, false
	}
	done := make(chan struct{})
	s.retiring[entry] = done
	return done, true
}
func (s *sharedAntigravityACP) retirementsLocked() []<-chan struct{} {
	waits := make([]<-chan struct{}, 0, len(s.retiring))
	for _, done := range s.retiring {
		waits = append(waits, done)
	}
	return waits
}
func (s *sharedAntigravityACP) finishRetirement(entry *antigravityACPConnection, abort bool, done chan struct{}, owner bool) {
	if !owner {
		if done != nil {
			<-done
		}
		return
	}
	entry.close(abort)
	s.mu.Lock()
	delete(s.retiring, entry)
	close(done)
	s.mu.Unlock()
}
func (s *sharedAntigravityACP) CloseService() {
	s.mu.Lock()
	if s.enabled {
		s.enabled = false
		s.epoch++
		close(s.done)
	}
	entry, opening := s.current, s.opening
	s.current = nil
	s.opening = nil
	done, owner := s.retireLocked(entry)
	waits := s.retirementsLocked()
	s.mu.Unlock()
	if opening != nil {
		opening.cancel()
	}
	if entry != nil {
		s.finishRetirement(entry, false, done, owner)
	}
	for _, wait := range waits {
		<-wait
	}
}
func (s *sharedAntigravityACP) discard(entry *antigravityACPConnection) {
	s.mu.Lock()
	if s.current == entry {
		s.current = nil
	}
	done, owner := s.retireLocked(entry)
	s.mu.Unlock()
	s.finishRetirement(entry, true, done, owner)
}
func (s *sharedAntigravityACP) owns(epoch uint64, entry *antigravityACPConnection) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled && s.epoch == epoch && s.current == entry
}

// getConnection runs under gate, never under the state lock during I/O.
func (s *sharedAntigravityACP) getConnection(ctx context.Context, bin string, epoch uint64) (*antigravityACPConnection, error) {
	s.mu.Lock()
	if !s.enabled || s.epoch != epoch {
		s.mu.Unlock()
		return nil, errAntigravityACPStopped
	}
	old := s.current
	if old != nil && old.bin == bin {
		s.mu.Unlock()
		return old, nil
	}
	s.current = nil
	retired, owner := s.retireLocked(old)
	retiring, done := s.retirementsLocked(), s.done
	s.mu.Unlock()
	if old != nil {
		s.finishRetirement(old, false, retired, owner)
	}
	for _, wait := range retiring {
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-done:
			return nil, errAntigravityACPStopped
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// This context belongs to the connection, not to the first query or UI.
	processCtx, cancel := context.WithCancel(context.Background())
	attempt := &antigravityACPOpenAttempt{cancel: cancel}
	s.mu.Lock()
	if !s.enabled || s.epoch != epoch {
		s.mu.Unlock()
		cancel()
		return nil, errAntigravityACPStopped
	}
	s.opening = attempt
	s.mu.Unlock()
	stopOpening := context.AfterFunc(ctx, cancel)
	conn, err := s.open(processCtx, bin)
	stopOpening()
	s.mu.Lock()
	valid := s.enabled && s.epoch == epoch && s.opening == attempt && ctx.Err() == nil && processCtx.Err() == nil
	if s.opening == attempt {
		s.opening = nil
	}
	if err == nil && valid {
		rpc := newMetadataRPCSession(processCtx, conn)
		rpc.protocol = "Antigravity ACP"
		rpc.jsonRPC = true
		entry := &antigravityACPConnection{bin: bin, conn: conn, rpc: rpc, cancel: cancel}
		s.current = entry
		s.mu.Unlock()
		return entry, nil
	}
	s.mu.Unlock()
	if conn != nil {
		entry := &antigravityACPConnection{conn: conn, cancel: cancel}
		entry.close(true)
	} else {
		cancel()
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return nil, errAntigravityACPStopped
}

func (s *sharedAntigravityACP) ListSessions(parent context.Context, bin string) (list []Session, err error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, antigravityHistoryTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	epoch, done, err := s.state()
	if err != nil {
		return nil, err
	}
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-done:
		return nil, errAntigravityACPStopped
	}
	defer func() { <-s.gate }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entry, err := s.getConnection(ctx, bin, epoch)
	if err != nil {
		return nil, err
	}
	watchDone := make(chan struct{})
	stopWatch := context.AfterFunc(ctx, func() { defer close(watchDone); s.discard(entry) })
	defer func() {
		if !stopWatch() {
			<-watchDone
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		} else if err == nil && !s.owns(epoch, entry) {
			err = errAntigravityACPStopped
		}
		if err != nil {
			list = nil
			s.discard(entry)
		}
	}()
	if !entry.initialized {
		if err = initializeAntigravityACP(ctx, entry.rpc); err != nil {
			return nil, err
		}
		entry.initialized = true
	}
	return listAntigravityACPSessions(ctx, entry.rpc)
}
