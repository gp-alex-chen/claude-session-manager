// Package terminal owns ConPTY processes and deliberately has no Wails import.
package terminal

import (
	"encoding/base64"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/gp-alex-chen/claude-session-manager/internal/session"
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
	lifecycleMu      sync.Mutex // Start/Kill/CloseAll/Adopt are serialized by this lock.
	mu               sync.Mutex // protects terms, dimensions, callbacks, and hooks.
	terms            map[string]*ptyRef
	persistIDByToken map[string]string
	cols, rows       int
	cb               Callbacks
	start            StartFunc
	persist          func([]string) error
	persistErr       func(error)
	persistDirty     bool // protected by mu; a failed snapshot needs a retry.
	persistRetryIDs  []string
}

func NewManager(cb Callbacks, persist func([]string) error) *Manager {
	return NewManagerWithStart(cb, persist, productionStart)
}

func NewManagerWithStart(cb Callbacks, persist func([]string) error, start StartFunc) *Manager {
	if start == nil {
		start = productionStart
	}
	return &Manager{
		terms:            make(map[string]*ptyRef),
		persistIDByToken: make(map[string]string),
		cols:             120,
		rows:             32,
		cb:               cb,
		start:            start,
		persist:          persist,
	}
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

func (m *Manager) persistSnapshot(ids []string) error {
	if m.persist == nil {
		return nil
	}
	return m.persist(append([]string(nil), ids...))
}

func (m *Manager) persistAndReport(ids []string) error {
	err := m.persistSnapshot(ids)
	m.mu.Lock()
	m.persistDirty = err != nil
	if err != nil {
		m.persistRetryIDs = append([]string(nil), ids...)
	} else {
		m.persistRetryIDs = nil
	}
	m.mu.Unlock()
	m.reportPersist(err)
	return err
}

// retryPersistenceLocked retries the last desired snapshot. The caller must
// hold lifecycleMu; persistence itself is kept outside m.mu because it may do
// filesystem I/O.
func (m *Manager) retryPersistenceLocked() error {
	m.mu.Lock()
	if !m.persistDirty {
		m.mu.Unlock()
		return nil
	}
	ids := append([]string(nil), m.persistRetryIDs...)
	m.mu.Unlock()
	return m.persistAndReport(ids)
}

func (m *Manager) persistWithRetry(ids []string) error {
	err := m.persistAndReport(ids)
	if err != nil {
		// Lifecycle teardown has no later callback that can guarantee a retry;
		// make one immediate attempt while retaining the failed snapshot if it
		// also fails.
		m.retryPersistenceLocked()
	}
	return err
}

// RetryPersistence retries the most recent snapshot that failed to reach
// disk. It is safe to call even when there is no pending failure.
func (m *Manager) RetryPersistence() error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	return m.retryPersistenceLocked()
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
	return m.openIDsLocked()
}

// OpenIDsWithPending adds IDs for adoption requests that have reached the App
// boundary but have not yet been associated with a live PTY. This closes the
// shutdown window between the Wails call and Manager.Adopt.
func (m *Manager) OpenIDsWithPending(pending []string) []string {
	m.mu.Lock()
	ids := m.openIDsLocked()
	m.mu.Unlock()
	return mergeIDs(ids, pending)
}

func mergeIDs(left, right []string) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	ids := make([]string, 0, len(left)+len(right))
	for _, id := range append(append([]string(nil), left...), right...) {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (m *Manager) openIDsLocked() []string {
	ids := make([]string, 0, len(m.terms))
	seen := make(map[string]struct{}, len(m.terms))
	for token, r := range m.terms {
		if token == "" || r == nil || r.Closed() {
			continue
		}
		id := token
		if strings.HasPrefix(token, "new-") {
			id = m.persistIDByToken[token]
			if id == "" {
				continue
			}
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Adopt associates a temporary runtime token with the real session ID that
// Claude created. The runtime token remains the identity used by the PTY and
// all terminal events; only persistence uses the adopted ID.
func (m *Manager) Adopt(token, sessionID string) error {
	token = strings.TrimSpace(token)
	sessionID = strings.TrimSpace(sessionID)
	if !strings.HasPrefix(token, "new-") {
		return errors.New("only temporary sessions can be adopted")
	}
	if err := session.ValidateID(sessionID); err != nil {
		return err
	}

	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()

	m.mu.Lock()
	r, ok := m.terms[token]
	if !ok || r == nil || r.Closed() {
		m.mu.Unlock()
		return errors.New("temporary session is not running")
	}
	if existing := m.persistIDByToken[token]; existing != "" {
		if existing != sessionID {
			m.mu.Unlock()
			return errors.New("temporary session already has a different adopted ID")
		}
		if !m.persistDirty {
			m.mu.Unlock()
			return nil
		}
		ids := m.openIDsLocked()
		m.mu.Unlock()
		return m.persistAndReport(ids)
	}
	for otherToken, other := range m.terms {
		if otherToken == token || other == nil || other.Closed() {
			continue
		}
		otherID := otherToken
		if adopted := m.persistIDByToken[otherToken]; adopted != "" {
			otherID = adopted
		}
		if otherID == sessionID {
			m.mu.Unlock()
			return errors.New("adopted session ID is already in use")
		}
	}
	if m.persistIDByToken == nil {
		m.persistIDByToken = make(map[string]string)
	}
	m.persistIDByToken[token] = sessionID
	ids := m.openIDsLocked()
	m.mu.Unlock()

	// Keep the in-memory adoption if disk I/O fails. The error is returned so
	// the frontend can retry the idempotent adoption before the process exits.
	return m.persistAndReport(ids)
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
	// Continue the requested lifecycle operation; its final snapshot below
	// supersedes this retry if it succeeds.
	m.retryPersistenceLocked()
	m.mu.Lock()
	r, ok := m.terms[token]
	var ids []string
	if ok {
		delete(m.terms, token)
		delete(m.persistIDByToken, token)
		ids = m.openIDsLocked()
	}
	m.mu.Unlock()
	if !ok {
		return
	}
	if r != nil {
		r.Close()
	}
	m.persistWithRetry(ids)
	if r == nil {
		return
	}
	if cb := m.callbacks(); cb.Exit != nil {
		cb.Exit(token)
	}
}

func (m *Manager) CloseAll() {
	m.CloseAllWithPendingAndForget(nil, nil)
}

// CloseAllWithPending closes all PTYs and persists the union of currently
// running sessions and adoption requests that are already in flight.
func (m *Manager) CloseAllWithPending(pending []string) {
	m.CloseAllWithPendingAndForget(pending, nil)
}

// CloseAllWithPendingAndForget is the coordinated shutdown primitive. It
// removes user-cancelled adoption identities and captures the final snapshot
// under the same lifecycle lock, so an in-flight Adopt cannot recreate an ID
// between those two operations.
func (m *Manager) CloseAllWithPendingAndForget(pending, forget []string) {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.Lock()
	terms := m.terms
	hasPending := false
	for _, id := range pending {
		if id != "" {
			hasPending = true
			break
		}
	}
	for _, token := range forget {
		delete(m.persistIDByToken, token)
	}
	if len(terms) == 0 && !hasPending {
		if len(forget) > 0 {
			m.mu.Unlock()
			m.persistWithRetry(nil)
			return
		}
		dirty := m.persistDirty
		m.mu.Unlock()
		if dirty {
			m.retryPersistenceLocked()
		}
		return
	}
	ids := mergeIDs(m.openIDsLocked(), pending)
	m.terms = make(map[string]*ptyRef)
	m.persistIDByToken = make(map[string]string)
	m.mu.Unlock()
	for _, r := range terms {
		if r != nil {
			r.Close()
		}
	}
	// Persist the snapshot captured before clearing the map. Reader teardown
	// waits on lifecycleMu and therefore cannot overwrite it with an empty set.
	if err := m.persistAndReport(ids); err != nil {
		// Shutdown/update is the last normal opportunity to make the restore
		// snapshot durable, so perform one immediate retry.
		m.retryPersistenceLocked()
	}
}

func (m *Manager) Start(token, cmdLine, dir string) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.retryPersistenceLocked()

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
	if old == nil {
		delete(m.persistIDByToken, token)
	}
	ids := m.openIDsLocked()
	m.mu.Unlock()
	if old != nil {
		old.Close()
	}
	// Persistence is auxiliary state. A disk error must not tear down the
	// successfully-created PTY (especially after an old instance was replaced).
	m.persistAndReport(ids)
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
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.retryPersistenceLocked()
	r.Close()
	m.mu.Lock()
	current := m.terms[token] == r
	var ids []string
	if current {
		delete(m.terms, token)
		delete(m.persistIDByToken, token)
		ids = m.openIDsLocked()
	}
	m.mu.Unlock()
	if !current {
		return
	}
	m.persistWithRetry(ids)
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
