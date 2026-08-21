// Package terminal owns ConPTY processes and deliberately has no Wails import.
package terminal

import (
	"encoding/base64"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/UserExistsError/conpty"
)

// Pty is the small process surface needed by Manager. Tests inject a fake;
// production uses the adapter around conpty.ConPty below.
type Pty interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Resize(int, int) error
	Close() error
}

// StartFunc creates one PTY. The manager holds lifecycleMu while invoking it,
// so starts, kills, and shutdown cannot race each other.
type StartFunc func(cmdLine, dir string, cols, rows int, env []string) (Pty, error)

type ptyRef struct {
	mu     sync.Mutex
	opMu   sync.Mutex // Write/Resize/Close are mutually exclusive at the call site.
	pty    Pty
	closed bool
}

func (r *ptyRef) Close() {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	r.mu.Lock()
	if r.closed || r.pty == nil {
		r.mu.Unlock()
		return
	}
	r.closed = true
	p := r.pty
	r.mu.Unlock()
	_ = p.Close()
}

func (r *ptyRef) Closed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

func (r *ptyRef) Write(p []byte) (int, error) {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	r.mu.Lock()
	pty, closed := r.pty, r.closed
	r.mu.Unlock()
	if pty == nil || closed {
		return 0, nil
	}
	return pty.Write(p)
}

func (r *ptyRef) Resize(cols, rows int) error {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	r.mu.Lock()
	pty, closed := r.pty, r.closed
	r.mu.Unlock()
	if pty == nil || closed {
		return nil
	}
	return pty.Resize(cols, rows)
}

// Read intentionally does not take opMu. It is the one long-lived reader and
// must remain interruptible by Close; only short Write/Resize calls are kept
// mutually exclusive with Close.
func (r *ptyRef) Read(buf []byte) (int, error) {
	r.mu.Lock()
	pty, closed := r.pty, r.closed
	r.mu.Unlock()
	if pty == nil || closed {
		return 0, errors.New("pty closed")
	}
	return pty.Read(buf)
}

type Callbacks struct {
	Data func(string, string)
	Exit func(string)
}

type Manager struct {
	lifecycleMu sync.Mutex // Start/Kill/CloseAll are serialized by this lock.
	mu          sync.Mutex // protects terms, dimensions, callbacks, and hooks.
	terms       map[string]*ptyRef
	cols, rows  int
	cb          Callbacks
	start       StartFunc
	persist     func() error
	persistErr  func(error)
}

func NewManager(cb Callbacks, persist func() error) *Manager {
	return NewManagerWithStart(cb, persist, productionStart)
}

func NewManagerWithStart(cb Callbacks, persist func() error, start StartFunc) *Manager {
	if start == nil {
		start = productionStart
	}
	return &Manager{terms: make(map[string]*ptyRef), cols: 120, rows: 32, cb: cb, start: start, persist: persist}
}

func productionStart(cmdLine, dir string, cols, rows int, env []string) (Pty, error) {
	return conpty.Start(cmdLine,
		conpty.ConPtyDimensions(cols, rows),
		conpty.ConPtyWorkDir(dir),
		conpty.ConPtyEnv(env),
	)
}

func (m *Manager) SetPersistErrorHandler(fn func(error)) {
	m.mu.Lock()
	m.persistErr = fn
	m.mu.Unlock()
}

func (m *Manager) reportPersist(err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	hook := m.persistErr
	m.mu.Unlock()
	if hook != nil {
		hook(err)
	}
}

func (m *Manager) persistOpen() error {
	if m.persist == nil {
		return nil
	}
	return m.persist()
}

func (m *Manager) SetCallbacks(cb Callbacks) {
	m.mu.Lock()
	m.cb = cb
	m.mu.Unlock()
}

func (m *Manager) callbacks() Callbacks {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cb
}

func (m *Manager) SetDimensions(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	m.mu.Lock()
	m.cols, m.rows = cols, rows
	m.mu.Unlock()
}

func (m *Manager) IsRunning(token string) bool {
	m.mu.Lock()
	r := m.terms[token]
	m.mu.Unlock()
	return r != nil && !r.Closed()
}

func (m *Manager) OpenIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.terms))
	for token, r := range m.terms {
		if token != "" && !strings.HasPrefix(token, "new-") && r != nil && !r.Closed() {
			ids = append(ids, token)
		}
	}
	sort.Strings(ids)
	return ids
}

func (m *Manager) Write(token string, raw []byte) {
	m.mu.Lock()
	r := m.terms[token]
	m.mu.Unlock()
	if r != nil {
		_, _ = r.Write(raw)
	}
}

func (m *Manager) Resize(token string, cols, rows int) {
	m.SetDimensions(cols, rows)
	m.mu.Lock()
	r := m.terms[token]
	m.mu.Unlock()
	if r != nil {
		_ = r.Resize(cols, rows)
	}
}

func (m *Manager) Kill(token string) {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.Lock()
	r, ok := m.terms[token]
	if ok {
		delete(m.terms, token)
	}
	m.mu.Unlock()
	if !ok || r == nil {
		return
	}
	r.Close()
	m.reportPersist(m.persistOpen())
	if cb := m.callbacks(); cb.Exit != nil {
		cb.Exit(token)
	}
}

func (m *Manager) CloseAll() {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.Lock()
	terms := m.terms
	m.terms = make(map[string]*ptyRef)
	m.mu.Unlock()
	for _, r := range terms {
		if r != nil {
			r.Close()
		}
	}
	m.reportPersist(m.persistOpen())
}

func (m *Manager) Start(token, cmdLine, dir string) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()

	m.mu.Lock()
	cols, rows := m.cols, m.rows
	old := m.terms[token]
	start := m.start
	m.mu.Unlock()
	env := make([]string, 0, len(os.Environ())+1)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "NO_COLOR=") {
			env = append(env, e)
		}
	}
	env = append(env, "TERM=xterm-256color")
	// Create first: if startup fails, old remains the current live instance.
	pty, err := start(cmdLine, dir, cols, rows, env)
	if err != nil {
		return err
	}
	r := &ptyRef{pty: pty}
	m.mu.Lock()
	m.terms[token] = r
	m.mu.Unlock()
	if old != nil {
		old.Close()
	}
	// Persistence is auxiliary state. A disk error must not tear down the
	// successfully-created PTY (especially after an old instance was replaced).
	m.reportPersist(m.persistOpen())
	m.readLoop(token, r)
	return nil
}

func (m *Manager) readLoop(token string, r *ptyRef) {
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				if cb := m.callbacks(); cb.Data != nil {
					cb.Data(token, base64.StdEncoding.EncodeToString(buf[:n]))
				}
			}
			if err != nil {
				break
			}
		}
		m.finishRead(token, r)
	}()
}

// finishRead is the synchronous reader teardown path. Identity is checked
// while holding m.mu so an old reader can never remove a replacement.
func (m *Manager) finishRead(token string, r *ptyRef) {
	r.Close()
	m.mu.Lock()
	current := m.terms[token] == r
	if current {
		delete(m.terms, token)
	}
	m.mu.Unlock()
	if !current {
		return
	}
	m.reportPersist(m.persistOpen())
	if cb := m.callbacks(); cb.Exit != nil {
		cb.Exit(token)
	}
}

func DecodeInput(b64 string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, errors.New("invalid terminal input")
	}
	return raw, nil
}
