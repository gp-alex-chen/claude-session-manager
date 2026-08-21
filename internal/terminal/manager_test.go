package terminal

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
)

type fakePty struct {
	mu          sync.Mutex
	closed      int
	writeStart  chan struct{}
	resizeStart chan struct{}
	allowWrite  chan struct{}
	allowResize chan struct{}
	read        chan struct{}
	readStart   chan struct{}
	readDone    chan struct{}
}

func (p *fakePty) Read([]byte) (int, error) {
	if p.readStart != nil {
		signal(p.readStart)
	}
	if p.read != nil {
		<-p.read
	}
	if p.readDone != nil {
		closeOnce(p.readDone)
	}
	return 0, io.EOF
}
func (p *fakePty) Write([]byte) (int, error) {
	if p.writeStart != nil {
		signal(p.writeStart)
		<-p.allowWrite
	}
	return 1, nil
}
func (p *fakePty) Resize(int, int) error {
	if p.resizeStart != nil {
		signal(p.resizeStart)
		<-p.allowResize
	}
	return nil
}
func (p *fakePty) Close() error {
	p.mu.Lock()
	p.closed++
	p.mu.Unlock()
	if p.read != nil {
		closeOnce(p.read)
	}
	return nil
}
func (p *fakePty) closeCount() int { p.mu.Lock(); defer p.mu.Unlock(); return p.closed }

func signal(ch chan struct{}) {
	select {
	case <-ch:
	default:
		close(ch)
	}
}
func closeOnce(ch chan struct{}) {
	select {
	case <-ch:
	default:
		close(ch)
	}
}

func TestPtyCloseIsIdempotent(t *testing.T) {
	p := &fakePty{}
	r := &ptyRef{pty: p}
	r.Close()
	r.Close()
	r.Close()
	if got := p.closeCount(); got != 1 {
		t.Fatalf("close count=%d, want 1", got)
	}
}

func TestWriteAndCloseAreMutuallyExclusive(t *testing.T) {
	p := &fakePty{writeStart: make(chan struct{}), allowWrite: make(chan struct{})}
	r := &ptyRef{pty: p}
	writeDone := make(chan struct{})
	go func() { _, _ = r.Write([]byte("x")); close(writeDone) }()
	<-p.writeStart
	closeDone := make(chan struct{})
	go func() { r.Close(); close(closeDone) }()
	select {
	case <-closeDone:
		t.Fatal("Close raced ahead of blocked Write")
	default:
	}
	close(p.allowWrite)
	<-writeDone
	<-closeDone
	if _, _ = r.Write([]byte("after")); p.closeCount() != 1 {
		t.Fatal("write after close touched PTY")
	}
}

func TestResizeAndCloseAreMutuallyExclusive(t *testing.T) {
	p := &fakePty{resizeStart: make(chan struct{}), allowResize: make(chan struct{})}
	r := &ptyRef{pty: p}
	resizeDone := make(chan struct{})
	go func() { _ = r.Resize(80, 24); close(resizeDone) }()
	<-p.resizeStart
	closeDone := make(chan struct{})
	go func() { r.Close(); close(closeDone) }()
	select {
	case <-closeDone:
		t.Fatal("Close raced ahead of blocked Resize")
	default:
	}
	close(p.allowResize)
	<-resizeDone
	<-closeDone
}

func TestStartFailureKeepsOldPTY(t *testing.T) {
	old := &fakePty{read: make(chan struct{})}
	var calls atomic.Int32
	start := func(cmd, dir string, cols, rows int, env []string) (Pty, error) {
		if calls.Add(1) == 2 {
			return nil, errors.New("start failed")
		}
		return old, nil
	}
	m := NewManagerWithStart(Callbacks{}, nil, start)
	if err := m.Start("session", "first", "."); err != nil {
		t.Fatal(err)
	}
	if err := m.Start("session", "second", "."); err == nil {
		t.Fatal("expected start failure")
	}
	if !m.IsRunning("session") {
		t.Fatal("old PTY was not retained")
	}
	if old.closeCount() != 0 {
		t.Fatal("old PTY was closed after failed replacement")
	}
	m.Kill("session")
}

func TestConcurrentStartsLeaveOneClosedAndOneCurrent(t *testing.T) {
	var calls atomic.Int32
	var mu sync.Mutex
	var made []*fakePty
	start := func(cmd, dir string, cols, rows int, env []string) (Pty, error) {
		p := &fakePty{read: make(chan struct{})}
		mu.Lock()
		made = append(made, p)
		mu.Unlock()
		calls.Add(1)
		return p, nil
	}
	m := NewManagerWithStart(Callbacks{}, nil, start)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = m.Start("same", "one", ".") }()
	go func() { defer wg.Done(); _ = m.Start("same", "two", ".") }()
	wg.Wait()
	if calls.Load() != 2 {
		t.Fatalf("start calls=%d, want 2", calls.Load())
	}
	mu.Lock()
	first, second := made[0], made[1]
	mu.Unlock()
	if first.closeCount()+second.closeCount() != 1 {
		t.Fatalf("closed counts=%d, want exactly one replaced PTY", first.closeCount()+second.closeCount())
	}
	if !m.IsRunning("same") {
		t.Fatal("no current PTY")
	}
	m.Kill("same")
}

func TestOldReaderCannotDeleteReplacementOrEmitExit(t *testing.T) {
	old := &fakePty{read: make(chan struct{}), readStart: make(chan struct{}), readDone: make(chan struct{})}
	next := &fakePty{read: make(chan struct{})}
	var calls atomic.Int32
	exits := make(chan string, 2)
	start := func(cmd, dir string, cols, rows int, env []string) (Pty, error) {
		if calls.Add(1) == 1 {
			return old, nil
		}
		return next, nil
	}
	m := NewManagerWithStart(Callbacks{Exit: func(token string) { exits <- token }}, nil, start)
	if err := m.Start("same", "one", "."); err != nil {
		t.Fatal(err)
	}
	<-old.readStart
	if err := m.Start("same", "two", "."); err != nil {
		t.Fatal(err)
	}
	<-old.readDone
	select {
	case got := <-exits:
		t.Fatalf("old reader emitted Exit for %q", got)
	default:
	}
	if !m.IsRunning("same") {
		t.Fatal("old reader removed replacement")
	}
	m.Kill("same")
}

func TestOpenIDsSortedAndFiltersTemporaryClosedAndNil(t *testing.T) {
	m := NewManagerWithStart(Callbacks{}, nil, nil)
	closed := &ptyRef{pty: &fakePty{}, closed: true}
	m.terms["session-b"] = &ptyRef{pty: &fakePty{}}
	m.terms["session-a"] = &ptyRef{pty: &fakePty{}}
	m.terms["new-temp"] = &ptyRef{pty: &fakePty{}}
	m.terms["closed"] = closed
	m.terms["nil"] = nil
	got := m.OpenIDs()
	if len(got) != 2 || got[0] != "session-a" || got[1] != "session-b" {
		t.Fatalf("OpenIDs=%v", got)
	}
}

func TestDecodeInputRejectsInvalidBase64(t *testing.T) {
	if _, err := DecodeInput("!"); err == nil {
		t.Fatal("expected invalid input error")
	}
}
