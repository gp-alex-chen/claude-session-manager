package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gp-alex-chen/claude-session-manager/internal/agent"
)

const antigravitySnapshotLimit = 256 << 10
const AntigravityHelperFlag = "--internal-antigravity-status"

// The native background updater can create an independent Windows console.
// Managed processes keep their executable stable; updates remain available from
// the user's own terminal. Never change the parent or global CLI preference.
const antigravityDisableAutoUpdateEnv = "AGY_CLI_DISABLE_AUTO_UPDATE=true"

var errAntigravitySnapshotBusy = errors.New("Antigravity snapshot is being committed")

type AntigravityConfig struct {
	Lookup           func(string) (string, error)
	Getenv           func(string) string
	HelperExecutable string
	UserSettingsPath string
	TempRoot         string
	OnChange         func()
	OnError          func(error)
	History          HistoryProvider
}

type antigravitySnapshot struct {
	ConversationID      string          `json:"conversation_id"`
	ConversationTitle   string          `json:"conversation_title"`
	AgentState          string          `json:"agent_state"`
	ConfirmationPending bool            `json:"tool_confirmation_pending"`
	Quota               json.RawMessage `json:"quota,omitempty"`
	PlanTier            string          `json:"plan_tier,omitempty"`
	QuotaObservedAt     int64           `json:"quota_observed_at,omitempty"`
}

// Only documented display fields cross this boundary. Credentials, email,
// transcripts and unverified usage/restore claims are deliberately not stored.
func WriteAntigravitySnapshot(input io.Reader, path string) error {
	data, err := io.ReadAll(io.LimitReader(input, antigravitySnapshotLimit+1))
	if err != nil {
		return err
	}
	if len(data) > antigravitySnapshotLimit {
		return errors.New("Antigravity status payload too large")
	}
	if trimmed := strings.TrimSpace(string(data)); !strings.HasPrefix(trimmed, "{") {
		return errors.New("Antigravity status must be a JSON object")
	}
	var snapshot antigravitySnapshot
	if err = json.Unmarshal(data, &snapshot); err != nil {
		return err
	}
	snapshot.Quota = sanitizeAntigravityQuota(snapshot.Quota)
	snapshot.PlanTier = antigravityTitle(snapshot.PlanTier)
	// Never accept a timestamp supplied in the CLI's payload.
	snapshot.QuotaObservedAt = time.Now().UnixMilli()
	data, err = json.Marshal(snapshot)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".status-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	unlock, err := lockAntigravitySnapshot(path, true)
	if err != nil {
		return err
	}
	defer unlock()
	return os.Rename(temp, path)
}

type antigravityRuntime struct {
	refreshMu    sync.Mutex
	dir          string
	snapshotPath string
	last         string
	info         agent.AgentInfo
	cancel       context.CancelFunc
	done         chan struct{}
	expectedID   string
	identitySeen bool
	ambiguous    bool
	quota        QuotaSnapshot
	bin          string
}

type AntigravityAdapter struct {
	config       AntigravityConfig
	metadata     *sharedAntigravityACP
	historyEpoch uint64
	mu           sync.Mutex
	runtimes     map[string]*antigravityRuntime
	historyGate  chan struct{}
	historyCache []Session
	historyAt    time.Time
	historyErr   error
	now          func() time.Time
}

func NewAntigravity(config AntigravityConfig) *AntigravityAdapter {
	if config.Lookup == nil {
		config.Lookup = exec.LookPath
	}
	if config.Getenv == nil {
		config.Getenv = os.Getenv
	}
	if config.HelperExecutable == "" {
		config.HelperExecutable, _ = os.Executable()
	}
	return &AntigravityAdapter{config: config, metadata: newSharedAntigravityACP(openAntigravityACPProcess), runtimes: make(map[string]*antigravityRuntime), historyGate: make(chan struct{}, 1), now: time.Now}
}

func (*AntigravityAdapter) Descriptor() Descriptor {
	return Descriptor{ID: Antigravity, Name: "Antigravity", NewSessionRestorable: true}
}
func (*AntigravityAdapter) ValidateSessionID(id string) error {
	if !uuidPattern.MatchString(id) {
		return errors.New("invalid Antigravity session UUID")
	}
	return nil
}

func (p *AntigravityAdapter) resolveExecutable() (string, error) {
	if bin, err := p.config.Lookup("agy"); err == nil && filepath.IsAbs(bin) {
		return bin, nil
	}
	root := p.config.Getenv("LOCALAPPDATA")
	if home := p.config.Getenv("USERPROFILE"); filepath.IsAbs(home) {
		bin := filepath.Join(home, ".local", "bin", "agy.exe")
		if st, err := os.Stat(bin); err == nil && !st.IsDir() {
			return bin, nil
		}
	}
	if filepath.IsAbs(root) {
		bin := filepath.Join(root, "agy", "bin", "agy.exe")
		if st, err := os.Stat(bin); err == nil && !st.IsDir() {
			return bin, nil
		}
	}
	return "", errors.New("未检测到 Antigravity CLI，请先安装 agy")
}

func (p *AntigravityAdapter) PrepareLaunch(ctx context.Context, req OpenRequest) (LaunchSpec, error) {
	return p.prepareLaunch(ctx, req, nil)
}

func (p *AntigravityAdapter) prepareLaunch(ctx context.Context, req OpenRequest, startup *antigravityStartupHistory) (LaunchSpec, error) {
	if strings.ContainsAny(req.RuntimeToken, "\x00\r\n") {
		return LaunchSpec{}, errors.New("invalid Antigravity runtime token")
	}
	resumeID := ""
	if req.Ref != nil {
		if req.Ref.Provider != Antigravity {
			return LaunchSpec{}, errors.New("session belongs to another agent")
		}
		if err := p.ValidateSessionID(req.Ref.ID); err != nil {
			return LaunchSpec{}, err
		}
		resumeID = req.Ref.ID
	}
	key, _ := EncodeKey(SessionRef{Antigravity, resumeID})
	if !strings.HasPrefix(req.RuntimeToken, "new-antigravity-") && (resumeID == "" || req.RuntimeToken != key) {
		return LaunchSpec{}, errors.New("invalid Antigravity runtime token")
	}
	bin, err := p.resolveExecutable()
	if err != nil {
		return LaunchSpec{}, err
	}
	if st, err := os.Stat(bin); err != nil || st.IsDir() {
		return LaunchSpec{}, errors.New("Antigravity 程序不可用")
	}
	if resumeID != "" {
		var list []Session
		if startup != nil {
			if startup.owner != p || startup.bin != bin || !p.now().Before(startup.expires) {
				return LaunchSpec{}, errors.New("启动恢复快照已过期或 CLI 程序已变化，请手动重新打开")
			}
			list = startup.sessions
		} else {
			list, err = p.readHistory(ctx, true, bin)
			if err != nil {
				return LaunchSpec{}, err
			}
		}
		if err := ctx.Err(); err != nil {
			return LaunchSpec{}, err
		}
		dir, err := normalizeAntigravityDirectory(req.Dir)
		if err != nil {
			return LaunchSpec{}, err
		}
		found := false
		for _, session := range list {
			if session.Ref.Provider == Antigravity && session.Ref.ID == resumeID {
				if !strings.EqualFold(session.Dir, dir) {
					return LaunchSpec{}, errors.New("项目目录与 Antigravity 原生会话不一致，请刷新历史")
				}
				found = true
				break
			}
		}
		if !found {
			return LaunchSpec{}, errors.New("Antigravity 原生历史中未找到该会话，请刷新历史；未启动新会话")
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return LaunchSpec{}, errors.New("Antigravity 会话的工作目录不存在或不可访问")
		}
	}
	if !filepath.IsAbs(p.config.HelperExecutable) {
		return LaunchSpec{}, errors.New("Antigravity status helper is unavailable")
	}
	if p.config.TempRoot != "" {
		if !filepath.IsAbs(p.config.TempRoot) {
			return LaunchSpec{}, errors.New("Antigravity runtime directory must be absolute")
		}
		if err = os.MkdirAll(p.config.TempRoot, 0700); err != nil {
			return LaunchSpec{}, err
		}
	}
	root, err := os.MkdirTemp(p.config.TempRoot, "theone-agy-")
	if err != nil {
		return LaunchSpec{}, err
	}
	fail := func(err error) (LaunchSpec, error) { os.RemoveAll(root); return LaunchSpec{}, err }
	settings := filepath.Join(root, "settings.json")
	// The shell expands a single environment variable instead of interpreting a
	// JSON string or an executable path as shell source. No global CLI settings
	// or project files are changed.
	// Keep the CLI's command string free of nested quotes. cmd resolves this
	// private shim from the terminal-only PATH; the shim safely quotes the host.
	helperName := "theone-antigravity-status.cmd"
	if err := os.WriteFile(filepath.Join(root, helperName), []byte("@echo off\r\n@\"%THEONE_AGY_HOST%\" "+AntigravityHelperFlag+"\r\n"), 0600); err != nil {
		return fail(err)
	}
	payload := map[string]any{"statusLine": map[string]any{"type": "command", "command": helperName, "stack_with_default": true}}
	// A session-level override would replace an existing custom command even
	// with stack_with_default. Preserve that customization and conservatively
	// leave TheOne's status unknown for this terminal instead.
	if p.config.UserSettingsPath != "" {
		settingsData, readErr := os.ReadFile(p.config.UserSettingsPath)
		if readErr != nil && !os.IsNotExist(readErr) {
			return fail(readErr)
		}
		if readErr == nil {
			var existing struct {
				StatusLine struct {
					Command string `json:"command"`
					Enabled *bool  `json:"enabled"`
				} `json:"statusLine"`
			}
			if err := json.Unmarshal(settingsData, &existing); err != nil {
				return fail(fmt.Errorf("读取 Antigravity 设置失败: %w", err))
			}
			if existing.StatusLine.Command != "" && (existing.StatusLine.Enabled == nil || *existing.StatusLine.Enabled) {
				payload = map[string]any{}
			}
		}
	}
	data, _ := json.Marshal(payload)
	if err = os.WriteFile(settings, data, 0600); err != nil {
		return fail(err)
	}
	if req.RuntimeContext != nil {
		ctx = req.RuntimeContext
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	r := &antigravityRuntime{dir: req.Dir, bin: bin, snapshotPath: filepath.Join(root, "snapshot.json"), cancel: cancel, done: make(chan struct{}), info: antigravityAgent(req.RuntimeToken, antigravitySnapshot{})}
	r.info.Cwd = req.Dir
	r.expectedID = resumeID
	if resumeID != "" {
		r.info.SessionID = resumeID
	}
	p.mu.Lock()
	if _, exists := p.runtimes[req.RuntimeToken]; exists {
		p.mu.Unlock()
		cancel()
		return fail(errors.New("Antigravity terminal already exists"))
	}
	p.runtimes[req.RuntimeToken] = r
	p.mu.Unlock()
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			cancel()
			<-r.done
			p.mu.Lock()
			if p.runtimes[req.RuntimeToken] == r {
				delete(p.runtimes, req.RuntimeToken)
			}
			p.mu.Unlock()
			p.removeRuntime(root)
			p.changed()
		})
	}
	go func() {
		defer close(r.done)
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.refresh(req.RuntimeToken, r)
			}
		}
	}()
	env := codexExecutableEnvironment(bin, nil)
	// cmd honours this flag when resolving a bare executable name. A project
	// cannot shadow the private shim with a same-named file in its directory.
	env[0] = "PATH=" + root + string(os.PathListSeparator) + strings.TrimPrefix(env[0], "PATH=")
	env = append(env, "THEONE_AGY_EXE="+bin, "THEONE_AGY_SETTINGS="+settings, "THEONE_AGY_HOST="+p.config.HelperExecutable, "THEONE_AGY_SNAPSHOT="+r.snapshotPath)
	env = append(env, "THEONE_AGY_CONVERSATION="+resumeID)
	env = append(env, "NoDefaultCurrentDirectoryInExePath=1", antigravityDisableAutoUpdateEnv)
	args := []string{"--settings", settings}
	if resumeID != "" {
		args = []string{"--conversation", resumeID, "--settings", settings}
	}
	return LaunchSpec{Command: "agy", Executable: bin, Args: args, Env: env, Close: cleanup}, nil
}

func (p *AntigravityAdapter) changed() {
	if p.config.OnChange != nil {
		p.config.OnChange()
	}
}

func (p *AntigravityAdapter) removeRuntime(root string) {
	if err := os.RemoveAll(root); err == nil {
		return
	}
	// A callback may still be finishing after the PTY closes. Retry only this
	// adapter-owned directory, without blocking the UI's close/shutdown path.
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.NewTimer(2 * time.Second)
		defer deadline.Stop()
		for {
			select {
			case <-ticker.C:
				if err := os.RemoveAll(root); err == nil {
					return
				}
			case <-deadline.C:
				if err := os.RemoveAll(root); err != nil && p.config.OnError != nil {
					p.config.OnError(err)
				}
				return
			}
		}
	}()
}

func (p *AntigravityAdapter) refresh(token string, r *antigravityRuntime) {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	unlock, lockErr := lockAntigravitySnapshot(r.snapshotPath, false)
	if errors.Is(lockErr, errAntigravitySnapshotBusy) {
		return
	}
	var data []byte
	err := lockErr
	if lockErr == nil {
		var f *os.File
		f, err = openAntigravitySnapshot(r.snapshotPath)
		if err == nil {
			data, err = io.ReadAll(io.LimitReader(f, antigravitySnapshotLimit+1))
			f.Close()
		}
	}
	if unlock != nil {
		unlock()
	}
	var snapshot antigravitySnapshot
	if err != nil || len(data) > antigravitySnapshotLimit || json.Unmarshal(data, &snapshot) != nil {
		snapshot = antigravitySnapshot{}
	}
	p.mu.Lock()
	if p.runtimes[token] != r || r.last == string(data) {
		p.mu.Unlock()
		return
	}
	r.last = string(data)
	r.quota = antigravityQuota(snapshot)
	r.info = antigravityAgent(token, snapshot)
	if r.expectedID == "" && uuidPattern.MatchString(snapshot.ConversationID) {
		r.expectedID = snapshot.ConversationID
	}
	if r.expectedID != "" {
		if snapshot.ConversationID == r.expectedID {
			r.identitySeen = true
		} else if snapshot.ConversationID != "" || r.identitySeen {
			r.ambiguous = true
		}
		r.info.SessionID = r.expectedID
		if !r.identitySeen || r.ambiguous {
			r.info.State = "unknown"
			r.info.Status = "unknown"
			r.info.Name = ""
			r.info.WaitingFor = ""
		}
	}
	r.info.Cwd = r.dir
	p.mu.Unlock()
	p.changed()
}

func antigravityAgent(token string, s antigravitySnapshot) agent.AgentInfo {
	name := antigravityTitle(s.ConversationTitle)
	info := agent.AgentInfo{Provider: string(Antigravity), SessionID: token, RuntimeToken: token, Kind: "interactive", Name: name, State: "unknown", Status: "unknown"}
	if uuidPattern.MatchString(s.ConversationID) {
		info.SessionID = s.ConversationID
	}
	switch s.AgentState {
	case "working", "tool_use":
		info.State = "working"
		info.Status = "busy"
	case "idle":
		info.State = "idle"
		info.Status = "idle"
	case "error":
		info.State = "failed"
		info.Status = "error"
	}
	if s.ConfirmationPending {
		info.State = "blocked"
		info.Status = "waiting"
		info.WaitingFor = "approval"
	}
	return info
}

func (p *AntigravityAdapter) Status(context.Context) ([]agent.AgentInfo, error) {
	// A direct query also observes the latest atomic snapshot. Tests need no
	// timer/sleep, and a paused event worker cannot leave a query behind.
	p.mu.Lock()
	runtimes := make(map[string]*antigravityRuntime, len(p.runtimes))
	for token, r := range p.runtimes {
		runtimes[token] = r
	}
	p.mu.Unlock()
	for token, r := range runtimes {
		p.refresh(token, r)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]agent.AgentInfo, 0, len(p.runtimes))
	for _, r := range p.runtimes {
		out = append(out, r.info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RuntimeToken < out[j].RuntimeToken })
	return out, nil
}

// RuntimeMatches is intentionally independent of persistence permission.
// Waiting for the expected ID can be reused; a switched runtime cannot.
func (p *AntigravityAdapter) RuntimeMatches(token, id string) bool {
	p.mu.Lock()
	r := p.runtimes[token]
	p.mu.Unlock()
	if r == nil {
		return false
	}
	p.refresh(token, r)
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runtimes[token] == r && r.expectedID == id && !r.ambiguous
}

// The executable and settings paths are passed as environment values, not
// inserted into shell source. This also handles spaces, Unicode, %, &, and !.
func AntigravityShellCommand(shell string, spec LaunchSpec) (string, error) {
	resume := len(spec.Args) == 4 && spec.Args[0] == "--conversation" && uuidPattern.MatchString(spec.Args[1]) && spec.Args[2] == "--settings"
	if spec.Command != "agy" || (!resume && (len(spec.Args) != 2 || spec.Args[0] != "--settings")) {
		return "", fmt.Errorf("invalid Antigravity launch specification")
	}
	if shell == "pwsh" {
		if resume {
			return `pwsh -NoLogo -NoProfile -NoExit -Command "& $env:THEONE_AGY_EXE --conversation $env:THEONE_AGY_CONVERSATION --settings $env:THEONE_AGY_SETTINGS"`, nil
		}
		return `pwsh -NoLogo -NoProfile -NoExit -Command "& $env:THEONE_AGY_EXE --settings $env:THEONE_AGY_SETTINGS"`, nil
	}
	if resume {
		return `cmd /d /v:off /s /c ""%THEONE_AGY_EXE%" --conversation "%THEONE_AGY_CONVERSATION%" --settings "%THEONE_AGY_SETTINGS%""`, nil
	}
	return `cmd /d /v:off /s /c ""%THEONE_AGY_EXE%" --settings "%THEONE_AGY_SETTINGS%""`, nil
}
