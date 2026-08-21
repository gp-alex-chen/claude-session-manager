// Package agent runs the claude agents --json status watcher.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

var debugMu sync.Mutex

func DebugLog(msg string) {
	debugMu.Lock()
	defer debugMu.Unlock()
	exe, err := os.Executable()
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(filepath.Dir(exe), "agents-debug.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	if st, _ := f.Stat(); st != nil && st.Size() > 600*1024 {
		_ = f.Truncate(0)
	}
	_, _ = fmt.Fprintf(f, "[%s] %s\n", time.Now().Format("15:04:05.000"), msg)
}

type AgentInfo struct {
	SessionID  string `json:"sessionId"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	State      string `json:"state"`
	Status     string `json:"status"`
	WaitingFor string `json:"waitingFor"`
	ID         string `json:"id"`
	PID        int    `json:"pid"`
	Cwd        string `json:"cwd"`
	StartedAt  int64  `json:"startedAt"`
}

type Fetcher func(context.Context) ([]AgentInfo, string)

func FetchFull(parent context.Context) ([]AgentInfo, string) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cmd", "/c", "claude", "agents", "--json")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	env := make([]string, 0, len(os.Environ())+1)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "NO_COLOR=") {
			env = append(env, e)
		}
	}
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return nil, "ERR: " + err.Error()
	}
	var list []AgentInfo
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, "ERR unmarshal: " + string(out)
	}
	return list, string(out)
}

func Signature(list []AgentInfo) string {
	parts := make([]string, 0, len(list))
	for _, a := range list {
		parts = append(parts, a.SessionID+"|"+a.State+"|"+a.Status+"|"+a.Kind)
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

func cloneAgents(in []AgentInfo) []AgentInfo { return append([]AgentInfo(nil), in...) }

type Watcher struct {
	lifecycleMu sync.Mutex
	running     bool
	cancel      context.CancelFunc
	done        chan struct{}
	cacheMu     sync.RWMutex
	cache       []AgentInfo
	sig         string
	cacheReady  bool
	fetch       Fetcher
	emit        func([]AgentInfo)
}

func NewWatcher(emit func([]AgentInfo)) *Watcher { return NewWatcherWithFetcher(FetchFull, emit) }
func NewWatcherWithFetcher(fetch Fetcher, emit func([]AgentInfo)) *Watcher {
	if fetch == nil {
		fetch = FetchFull
	}
	return &Watcher{fetch: fetch, emit: emit}
}

func (w *Watcher) Start(parent context.Context) {
	if parent == nil {
		parent = context.Background()
	}
	w.lifecycleMu.Lock()
	if w.running {
		w.lifecycleMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	w.running, w.cancel, w.done = true, cancel, done
	w.lifecycleMu.Unlock()
	go w.run(ctx, done)
}

func (w *Watcher) Stop() {
	w.lifecycleMu.Lock()
	if !w.running {
		w.lifecycleMu.Unlock()
		return
	}
	cancel, done := w.cancel, w.done
	w.lifecycleMu.Unlock()
	cancel()
	<-done
	w.lifecycleMu.Lock()
	if w.done == done {
		w.running, w.cancel, w.done = false, nil, nil
	}
	w.lifecycleMu.Unlock()
}

func (w *Watcher) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	fail := 0
	for {
		interval := w.pollOnce(ctx, &fail)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
	}
}

func (w *Watcher) pollOnce(ctx context.Context, fail *int) time.Duration {
	list, raw := w.fetch(ctx)
	if list == nil {
		(*fail)++
		if *fail >= 3 {
			return 5 * time.Second
		}
		return 2 * time.Second
	}
	*fail = 0
	list = cloneAgents(list)
	sig := Signature(list)
	w.cacheMu.Lock()
	changed := sig != w.sig
	w.cache, w.sig, w.cacheReady = cloneAgents(list), sig, true
	w.cacheMu.Unlock()
	if len(list) > 0 {
		DebugLog("watch raw=" + raw)
	}
	if changed && w.emit != nil {
		DebugLog("状态变化，push agents:update n=" + fmt.Sprint(len(list)))
		w.emit(cloneAgents(list))
	}
	if len(list) > 0 {
		return time.Second
	}
	return 2 * time.Second
}

func (w *Watcher) Snapshot() []AgentInfo {
	snapshot, _ := w.snapshot()
	return snapshot
}

func (w *Watcher) snapshot() ([]AgentInfo, bool) {
	w.cacheMu.RLock()
	defer w.cacheMu.RUnlock()
	return cloneAgents(w.cache), w.cacheReady
}

func (w *Watcher) Get() []AgentInfo { return w.GetContext(context.Background()) }
func (w *Watcher) GetContext(ctx context.Context) []AgentInfo {
	if ctx == nil {
		ctx = context.Background()
	}
	if got, ready := w.snapshot(); ready {
		return got
	}
	list, raw := w.fetch(ctx)
	if list == nil {
		DebugLog("GetAgents 首次拉取失败 raw=" + raw)
		return nil
	}
	list = cloneAgents(list)
	w.cacheMu.Lock()
	w.cache, w.sig, w.cacheReady = cloneAgents(list), Signature(list), true
	w.cacheMu.Unlock()
	DebugLog("GetAgents 首次拉取 len=" + fmt.Sprint(len(list)) + " raw=" + raw)
	return cloneAgents(list)
}
