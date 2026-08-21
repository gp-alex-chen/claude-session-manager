package terminal

import (
	"errors"
	"io"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

type fakePty struct {
	mu          sync.Mutex
	closed      int
	writes      int
	resizes     int
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
	p.mu.Lock()
	p.writes++
	p.mu.Unlock()
	if p.writeStart != nil {
		signal(p.writeStart)
		<-p.allowWrite
	}
	return 1, nil
}
func (p *fakePty) Resize(int, int) error {
	p.mu.Lock()
	p.resizes++
	p.mu.Unlock()
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
func (p *fakePty) closeCount() int  { p.mu.Lock(); defer p.mu.Unlock(); return p.closed }
func (p *fakePty) writeCount() int  { p.mu.Lock(); defer p.mu.Unlock(); return p.writes }
func (p *fakePty) resizeCount() int { p.mu.Lock(); defer p.mu.Unlock(); return p.resizes }

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
	if got := p.writeCount(); got != 1 {
		t.Fatalf("blocked Write count=%d, want 1", got)
	}
	_, _ = r.Write([]byte("after"))
	if got := p.writeCount(); got != 1 {
		t.Fatalf("Write after Close touched PTY, count=%d", got)
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
	if got := p.resizeCount(); got != 1 {
		t.Fatalf("blocked Resize count=%d, want 1", got)
	}
	_ = r.Resize(80, 24)
	if got := p.resizeCount(); got != 1 {
		t.Fatalf("Resize after Close touched PTY, count=%d", got)
	}
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
	m.mu.Lock()
	current := m.terms["session"]
	m.mu.Unlock()
	if current == nil || current.pty != old {
		t.Fatal("old PTY identity was not retained after failed replacement")
	}
	if old.closeCount() != 0 {
		t.Fatal("old PTY was closed after failed replacement")
	}
	m.Kill("session")
}

func TestStartPersistsErrorWithoutTearingDownPTY(t *testing.T) {
	sentinel := errors.New("persist failed")
	p := &fakePty{read: make(chan struct{})}
	var reported atomic.Int32
	m := NewManagerWithStart(Callbacks{}, func([]string) error { return sentinel }, func(string, string, int, int, []string) (Pty, error) { return p, nil })
	m.SetPersistErrorHandler(func(err error) {
		if errors.Is(err, sentinel) {
			reported.Add(1)
		}
	})
	if err := m.Start("session", "cmd", "."); err != nil {
		t.Fatalf("Start returned persistence error: %v", err)
	}
	if !m.IsRunning("session") {
		t.Fatal("PTY was torn down after persistence error")
	}
	m.mu.Lock()
	current := m.terms["session"]
	m.mu.Unlock()
	if current == nil || current.pty != p {
		t.Fatal("PTY is not the current identity after persistence error")
	}
	if got := reported.Load(); got != 1 {
		t.Fatalf("persist error reports=%d, want 1", got)
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
	old := &fakePty{}
	next := &fakePty{}
	var persistCalls atomic.Int32
	exits := make(chan string, 2)
	m := NewManagerWithStart(Callbacks{Exit: func(token string) { exits <- token }}, func([]string) error { persistCalls.Add(1); return nil }, nil)
	oldRef := &ptyRef{pty: old}
	nextRef := &ptyRef{pty: next}
	m.terms["same"] = nextRef
	m.finishRead("same", oldRef)
	if persistCalls.Load() != 0 {
		t.Fatal("old reader persisted after replacement")
	}
	select {
	case got := <-exits:
		t.Fatalf("old reader emitted Exit for %q", got)
	default:
	}
	if !m.IsRunning("same") {
		t.Fatal("old reader removed replacement")
	}
	m.mu.Lock()
	if m.terms["same"] != nextRef {
		t.Fatal("replacement identity changed")
	}
	m.mu.Unlock()
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

func TestCloseAllPersistsOpenIDsBeforeClearing(t *testing.T) {
	var persisted [][]string
	m := NewManagerWithStart(Callbacks{}, func(ids []string) error {
		persisted = append(persisted, ids)
		return nil
	}, nil)
	first := &ptyRef{pty: &fakePty{}}
	second := &ptyRef{pty: &fakePty{}}
	m.terms["session-b"] = second
	m.terms["session-a"] = first
	m.terms["new-temporary"] = &ptyRef{pty: &fakePty{}}
	m.terms["closed"] = &ptyRef{pty: &fakePty{}, closed: true}
	m.terms[""] = &ptyRef{pty: &fakePty{}}
	m.terms["nil"] = nil

	m.CloseAll()
	if len(persisted) != 1 || !reflect.DeepEqual(persisted[0], []string{"session-a", "session-b"}) {
		t.Fatalf("persisted snapshots = %v", persisted)
	}

	// A reader finishing after CloseAll must not write the now-empty manager
	// state over the shutdown snapshot.
	m.finishRead("session-a", first)
	if len(persisted) != 1 {
		t.Fatalf("reader teardown persisted an extra snapshot: %v", persisted)
	}
}

func TestDecodeInputRejectsInvalidBase64(t *testing.T) {
	if _, err := DecodeInput("!"); err == nil {
		t.Fatal("expected invalid input error")
	}
}
