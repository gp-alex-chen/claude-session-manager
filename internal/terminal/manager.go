// Package terminal owns ConPTY processes and deliberately has no Wails import.
package terminal

import (
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/UserExistsError/conpty"
)

type ptyRef struct {
	mu     sync.Mutex
	pty    *conpty.ConPty
	closed bool
}

func (r *ptyRef) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.pty == nil {
		return
	}
	r.closed = true
	_ = r.pty.Close()
}
func (r *ptyRef) Closed() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.closed }
func (r *ptyRef) Write(p []byte) (int, error) {
	r.mu.Lock()
	pty, closed := r.pty, r.closed
	r.mu.Unlock()
	if pty == nil || closed {
		return 0, nil
	}
	return pty.Write(p)
}
func (r *ptyRef) Resize(c, rows int) error {
	r.mu.Lock()
	pty, closed := r.pty, r.closed
	r.mu.Unlock()
	if pty == nil || closed {
		return nil
	}
	return pty.Resize(c, rows)
}

type Callbacks struct {
	Data func(string, string)
	Exit func(string)
}
type Manager struct {
	mu         sync.Mutex
	terms      map[string]*ptyRef
	cols, rows int
	cb         Callbacks
	persist    func()
}

func (m *Manager) persistOpen() {
	if m.persist != nil {
		m.persist()
	}
}

func NewManager(cb Callbacks, persist func()) *Manager {
	return &Manager{terms: map[string]*ptyRef{}, cols: 120, rows: 32, cb: cb, persist: persist}
}
func (m *Manager) SetCallbacks(cb Callbacks) { m.mu.Lock(); m.cb = cb; m.mu.Unlock() }
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
	out := make([]string, 0, len(m.terms))
	for token, r := range m.terms {
		if !strings.HasPrefix(token, "new-") && !r.Closed() {
			out = append(out, token)
		}
	}
	return out
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
	m.mu.Lock()
	r, ok := m.terms[token]
	if ok {
		delete(m.terms, token)
	}
	cb := m.cb
	m.mu.Unlock()
	if !ok {
		return
	}
	r.Close()
	m.persistOpen()
	if cb.Exit != nil {
		cb.Exit(token)
	}
}
func (m *Manager) CloseAll() {
	m.mu.Lock()
	terms := m.terms
	m.terms = map[string]*ptyRef{}
	m.mu.Unlock()
	for _, r := range terms {
		r.Close()
	}
	m.persistOpen()
}

func (m *Manager) Start(token, cmdLine, dir string) error {
	m.mu.Lock()
	if old := m.terms[token]; old != nil {
		old.Close()
		delete(m.terms, token)
	}
	cols, rows := m.cols, m.rows
	m.mu.Unlock()
	env := make([]string, 0, len(os.Environ())+1)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "NO_COLOR=") {
			env = append(env, e)
		}
	}
	env = append(env, "TERM=xterm-256color")
	pty, err := conpty.Start(cmdLine, conpty.ConPtyDimensions(cols, rows), conpty.ConPtyWorkDir(dir), conpty.ConPtyEnv(env))
	if err != nil {
		return err
	}
	r := &ptyRef{pty: pty}
	m.mu.Lock()
	m.terms[token] = r
	cb := m.cb
	m.mu.Unlock()
	m.persistOpen()
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := r.pty.Read(buf)
			if n > 0 && cb.Data != nil {
				cb.Data(token, base64.StdEncoding.EncodeToString(buf[:n]))
			}
			if err != nil {
				break
			}
		}
		r.Close()
		m.mu.Lock()
		current := m.terms[token] == r
		if current {
			delete(m.terms, token)
		}
		cb = m.cb
		m.mu.Unlock()
		if current {
			m.persistOpen()
			if cb.Exit != nil {
				cb.Exit(token)
			}
		}
	}()
	return nil
}

func DecodeInput(b64 string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, errors.New("invalid terminal input")
	}
	return raw, nil
}
