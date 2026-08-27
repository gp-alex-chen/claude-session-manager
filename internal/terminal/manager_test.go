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

func TestAdoptedTemporarySessionPersistsRealIDWithoutChangingRuntimeToken(t *testing.T) {
	var persisted [][]string
	p := &fakePty{read: make(chan struct{})}
	m := NewManagerWithStart(Callbacks{}, func(ids []string) error {
		persisted = append(persisted, append([]string(nil), ids...))
		return nil
	}, func(string, string, int, int, []string) (Pty, error) {
		return p, nil
	})

	if err := m.Start("new-1", "cmd", "."); err != nil {
		t.Fatal(err)
	}
	persisted = nil

	if err := m.Adopt("new-1", "real-1"); err != nil {
		t.Fatal(err)
	}
	if got := m.OpenIDs(); !reflect.DeepEqual(got, []string{"real-1"}) {
		t.Fatalf("OpenIDs after adoption = %v, want [real-1]", got)
	}
	m.mu.Lock()
	current := m.terms["new-1"]
	m.mu.Unlock()
	if current == nil || current.pty != p {
		t.Fatal("adoption changed the runtime token or PTY identity")
	}

	m.CloseAll()
	if len(persisted) == 0 || !reflect.DeepEqual(persisted[len(persisted)-1], []string{"real-1"}) {
		t.Fatalf("persisted snapshots = %v, want the final snapshot to contain [real-1]", persisted)
	}
}

func TestAdoptionPersistenceErrorIsReturnedAndRetriedIdempotently(t *testing.T) {
	sentinel := errors.New("persist failed")
	var shouldFail atomic.Bool
	var persistCalls atomic.Int32
	m := NewManagerWithStart(Callbacks{}, func([]string) error {
		persistCalls.Add(1)
		if shouldFail.Load() {
			return sentinel
		}
		return nil
	}, func(string, string, int, int, []string) (Pty, error) {
		return &fakePty{read: make(chan struct{})}, nil
	})

	if err := m.Start("new-retry", "cmd", "."); err != nil {
		t.Fatal(err)
	}
	shouldFail.Store(true)
	if err := m.Adopt("new-retry", "real-retry"); !errors.Is(err, sentinel) {
		t.Fatalf("first adoption error = %v, want %v", err, sentinel)
	}
	if got := m.OpenIDs(); !reflect.DeepEqual(got, []string{"real-retry"}) {
		t.Fatalf("OpenIDs after failed adoption = %v", got)
	}

	shouldFail.Store(false)
	if err := m.Adopt("new-retry", "real-retry"); err != nil {
		t.Fatalf("idempotent adoption retry failed: %v", err)
	}
	if got := persistCalls.Load(); got != 3 {
		t.Fatalf("persist calls = %d, want start + failed adoption + retry", got)
	}
	m.CloseAll()
}

func TestAdoptedTemporarySessionKeepsIdentityAcrossReplacement(t *testing.T) {
	first := &fakePty{read: make(chan struct{})}
	second := &fakePty{read: make(chan struct{})}
	var starts atomic.Int32
	m := NewManagerWithStart(Callbacks{}, nil, func(string, string, int, int, []string) (Pty, error) {
		if starts.Add(1) == 1 {
			return first, nil
		}
		return second, nil
	})

	if err := m.Start("new-replacement", "cmd", "."); err != nil {
		t.Fatal(err)
	}
	if err := m.Adopt("new-replacement", "real-replacement"); err != nil {
		t.Fatal(err)
	}
	if err := m.Start("new-replacement", "cmd", "."); err != nil {
		t.Fatal(err)
	}
	if got := m.OpenIDs(); !reflect.DeepEqual(got, []string{"real-replacement"}) {
		t.Fatalf("OpenIDs after adopted replacement = %v, want [real-replacement]", got)
	}
	m.CloseAll()
}

func TestAdoptedOldReaderCannotDeleteReplacement(t *testing.T) {
	old := &ptyRef{pty: &fakePty{}}
	next := &ptyRef{pty: &fakePty{}}
	m := NewManagerWithStart(Callbacks{}, nil, nil)
	m.terms["new-reader"] = next
	m.persistIDByToken["new-reader"] = "real-reader"

	m.finishRead("new-reader", old)

	if got := m.OpenIDs(); !reflect.DeepEqual(got, []string{"real-reader"}) {
		t.Fatalf("OpenIDs after old adopted reader exit = %v, want [real-reader]", got)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.terms["new-reader"] != next || m.persistIDByToken["new-reader"] != "real-reader" {
		t.Fatal("old adopted reader changed the replacement identity")
	}
}

func TestPersistenceFailureCanBeRetriedAfterKill(t *testing.T) {
	sentinel := errors.New("persist failed")
	var failures atomic.Int32
	var calls atomic.Int32
	m := NewManagerWithStart(Callbacks{}, func(ids []string) error {
		calls.Add(1)
		if failures.Load() > 0 && failures.Add(-1) >= 0 {
			return sentinel
		}
		return nil
	}, func(string, string, int, int, []string) (Pty, error) {
		return &fakePty{read: make(chan struct{})}, nil
	})

	if err := m.Start("kill-retry", "cmd", "."); err != nil {
		t.Fatal(err)
	}
	failures.Store(2)
	m.Kill("kill-retry")
	if got := calls.Load(); got != 3 {
		t.Fatalf("calls after failed kill = %d, want start + kill + immediate retry", got)
	}
	if err := m.RetryPersistence(); err != nil {
		t.Fatalf("RetryPersistence failed: %v", err)
	}
	if got := calls.Load(); got != 4 {
		t.Fatalf("calls after retry = %d, want 4", got)
	}
	if got := m.OpenIDs(); len(got) != 0 {
		t.Fatalf("OpenIDs after kill = %v, want empty", got)
	}
}

func TestCloseAllRetriesFailedRestoreSnapshot(t *testing.T) {
	sentinel := errors.New("persist failed")
	var calls atomic.Int32
	m := NewManagerWithStart(Callbacks{}, func(ids []string) error {
		if calls.Add(1) == 1 {
			return sentinel
		}
		return nil
	}, nil)
	m.terms["restore-me"] = &ptyRef{pty: &fakePty{}}

	m.CloseAll()
	if got := calls.Load(); got != 2 {
		t.Fatalf("CloseAll persistence calls = %d, want failed call + immediate retry", got)
	}
	if err := m.RetryPersistence(); err != nil {
		t.Fatalf("unexpected pending persistence after successful retry: %v", err)
	}
}

func TestCloseAllIsIdempotentWithoutOverwritingRestoreSnapshot(t *testing.T) {
	var persisted [][]string
	m := NewManagerWithStart(Callbacks{}, func(ids []string) error {
		persisted = append(persisted, append([]string(nil), ids...))
		return nil
	}, nil)
	m.terms["restore-me"] = &ptyRef{pty: &fakePty{}}

	m.CloseAll()
	m.CloseAll()
	if len(persisted) != 1 || !reflect.DeepEqual(persisted[0], []string{"restore-me"}) {
		t.Fatalf("idempotent CloseAll snapshots = %v", persisted)
	}
}

func TestCloseAllWithPendingAndForgetExcludesCancelledAdoption(t *testing.T) {
	var persisted [][]string
	m := NewManagerWithStart(Callbacks{}, func(ids []string) error {
		persisted = append(persisted, append([]string(nil), ids...))
		return nil
	}, nil)
	m.terms["new-cancelled"] = &ptyRef{pty: &fakePty{}}
	m.persistIDByToken["new-cancelled"] = "real-cancelled"

	m.CloseAllWithPendingAndForget(nil, []string{"new-cancelled"})
	if len(persisted) != 1 || len(persisted[0]) != 0 {
		t.Fatalf("cancelled adoption snapshot = %v, want empty", persisted)
	}
}

func TestCancelledAdoptionOverridesStaleFailedSnapshotWhenAlreadyClosed(t *testing.T) {
	var persisted [][]string
	m := NewManagerWithStart(Callbacks{}, func(ids []string) error {
		persisted = append(persisted, append([]string(nil), ids...))
		return nil
	}, nil)
	m.persistDirty = true
	m.persistRetryIDs = []string{"stale-real"}

	m.CloseAllWithPendingAndForget(nil, []string{"new-cancelled"})
	if len(persisted) != 1 || len(persisted[0]) != 0 {
		t.Fatalf("cancelled stale snapshot = %v, want empty", persisted)
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
