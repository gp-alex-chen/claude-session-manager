package main

// 会话数据层：解析 ~/.claude/projects/**/*.jsonl（与 PowerShell 版同源逻辑）

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Session 一个 claude 会话
type Session struct {
	ID     string
	Dir    string
	Time   time.Time // 文件修改时间（行尾显示"最近使用"）
	Text   string
	Name   string
	IsSide bool
}

type project struct {
	Name     string
	Sessions []Session
}

var (
	reTitle = regexp.MustCompile(`"type":"(ai-title|agent-name)"`)
	reName  = regexp.MustCompile(`"aiTitle":"([^"]*)"|"agentName":"([^"]*)"`)
	reProj  = regexp.MustCompile(`^([A-Za-z])--(.+)$`)
	reSafe  = regexp.MustCompile(`[^A-Za-z0-9_-]`)
)

// jsonl 行结构化（只取需要的字段）
type line struct {
	Type      string          `json:"type"`
	AiTitle   string          `json:"aiTitle"`
	AgentName string          `json:"agentName"`
	Cwd       string          `json:"cwd"`
	IsSide    bool            `json:"isSidechain"`
	Message   json.RawMessage `json:"message"`
}

// decodeDir 项目目录名解码：C--Users--x -> C:\Users\x（带合并兜底）
func decodeDir(enc string) string {
	m := reProj.FindStringSubmatch(enc)
	if m == nil {
		return enc
	}
	drive := m[1] + ":"
	rest := m[2]
	simple := drive + "\\" + strings.ReplaceAll(rest, "-", "\\")
	if _, err := os.Stat(simple); err == nil {
		return simple
	}
	if idx := strings.LastIndex(rest, "-"); idx > 0 {
		merged := drive + "\\" + strings.ReplaceAll(rest[:idx], "-", "\\") + rest[idx:]
		if _, err := os.Stat(merged); err == nil {
			return merged
		}
	}
	return simple
}

func parseSession(path, fallbackDir string) *Session {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	s := &Session{
		ID:  strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Dir: fallbackDir,
	}
	if fi, err := f.Stat(); err == nil {
		s.Time = fi.ModTime()
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 32<<20)
	n := 0
	for sc.Scan() {
		n++
		if n > 5000 || (s.Name != "" && s.Text != "") {
			break
		}
		raw := sc.Bytes()
		if s.Name == "" && reTitle.Match(raw) {
			if m := reName.FindSubmatch(raw); m != nil {
				if len(m[1]) > 0 {
					s.Name = string(m[1])
				} else {
					s.Name = string(m[2])
				}
			}
		}
		if s.Text != "" || !bytes.Contains(raw, []byte(`"type":"user"`)) || len(raw) > 512000 {
			continue
		}
		var l line
		if err := json.Unmarshal(raw, &l); err != nil || l.Type != "user" {
			continue
		}
		if l.Cwd != "" {
			s.Dir = l.Cwd
		}
		s.IsSide = l.IsSide
		if len(l.Message) > 0 {
			var str string
			if err := json.Unmarshal(l.Message, &str); err == nil {
				s.Text = str
			} else {
				var blocks []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				if err := json.Unmarshal(l.Message, &blocks); err == nil {
					for _, b := range blocks {
						if b.Type == "text" && b.Text != "" {
							s.Text = b.Text
							break
						}
					}
				}
			}
		}
	}
	return s
}

func scanAll() []*Session {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	root := filepath.Join(home, ".claude", "projects")
	projDirs, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []*Session
	for _, pd := range projDirs {
		if !pd.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, pd.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			if fi, err := f.Info(); err != nil || fi.Size() == 0 {
				continue
			}
			path := filepath.Join(root, pd.Name(), f.Name())
			if s := parseSession(path, decodeDir(pd.Name())); s != nil {
				out = append(out, s)
			}
		}
	}
	return out
}

// groupSessions 按目录分组。组按目录名升序（不区分大小写）；
// 组内会话按修改时间降序（最近使用在前，同刻按 ID 兜底保证稳定）。
func groupSessions(list []*Session) []*project {
	m := map[string]*project{}
	var order []string
	for _, s := range list {
		p, ok := m[s.Dir]
		if !ok {
			p = &project{Name: s.Dir}
			m[s.Dir] = p
			order = append(order, s.Dir)
		}
		p.Sessions = append(p.Sessions, *s)
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := strings.ToLower(order[i]), strings.ToLower(order[j])
		if a == b {
			return order[i] < order[j]
		}
		return a < b
	})
	out := make([]*project, 0, len(order))
	for _, k := range order {
		p := m[k]
		sort.SliceStable(p.Sessions, func(i, j int) bool {
			if p.Sessions[i].Time.Equal(p.Sessions[j].Time) {
				return p.Sessions[i].ID < p.Sessions[j].ID
			}
			return p.Sessions[i].Time.After(p.Sessions[j].Time)
		})
		out = append(out, p)
	}
	return out
}

// displayName 会话显示名：ai-title -> 首条消息摘要（≤48 字符）
func displayName(s *Session) string {
	if s.Name != "" {
		return s.Name
	}
	t := strings.TrimSpace(strings.Join(strings.Fields(s.Text), " "))
	if t == "" {
		return "(无摘要)"
	}
	r := []rune(t)
	if len(r) > 48 {
		t = string(r[:48]) + "…"
	}
	return t
}

func sanitize(s string) string {
	s = reSafe.ReplaceAllString(s, "-")
	if s == "" {
		s = "claude"
	}
	return s
}

// fileSignature 所有会话文件的快照签名（路径+大小+修改时间），用于自动刷新检测
func fileSignature() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	root := filepath.Join(home, ".claude", "projects")
	projDirs, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	var sb strings.Builder
	for _, pd := range projDirs {
		if !pd.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, pd.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			fi, err := f.Info()
			if err != nil {
				continue
			}
			sb.WriteString(f.Name())
			sb.WriteString(":")
			sb.WriteString(strconv.FormatInt(fi.Size(), 10))
			sb.WriteString(":")
			sb.WriteString(strconv.FormatInt(fi.ModTime().UnixNano(), 10))
			sb.WriteString(";")
		}
	}
	return sb.String()
}

// fetchAgents 查询正在运行的 claude 会话（sessionId -> kind: interactive/background）
func fetchAgents() map[string]string {
	m := map[string]string{}
	for _, a := range fetchAgentsFull() {
		if a.SessionID != "" && a.Kind != "" {
			m[a.SessionID] = a.Kind
		}
	}
	return m
}

// openSession 在新终端窗口恢复会话（跨平台）：直接运行本程序 -run 模式，
// 由 runner.go 完成 cd / 活动检查 / 执行 claude，不再依赖 resume-session.ps1
func openSession(s *Session) error {
	exe, err := os.Executable()
	if err != nil {
		exe = "claude-sidebar"
	}
	start := func(name string, arg ...string) error {
		guiLog("openSession: %s %s -> dir=%s", name, strings.Join(arg, " "), s.Dir)
		if err := exec.Command(name, arg...).Start(); err != nil {
			guiLog("openSession 失败: %v", err)
			return err
		}
		return nil
	}
	switch runtime.GOOS {
	case "windows":
		leaf := sanitize(filepath.Base(s.Dir))
		if strings.HasPrefix(leaf, "-") {
			leaf = "c" + leaf // wt 会把以 - 开头的值当参数解析
		}
		// wt 可用且路径无空格 → wt new-tab（参数必须无空格）
		if wt, err := exec.LookPath("wt"); err == nil && !strings.ContainsAny(exe, " ") {
			return start(wt, "new-tab", "--title", leaf, exe, "-run", s.ID)
		}
		// 兜底：新控制台窗口直接运行本程序 -run 模式
		return start("cmd", "/c", "start", "", `"`+exe+`"`, "-run", s.ID)
	case "darwin":
		// macOS：AppleScript 在 Terminal 新窗口执行
		script := fmt.Sprintf(`tell application "Terminal" to do script "cd %s && %s -run %s"`,
			shellQuote(s.Dir), shellQuote(exe), s.ID)
		return exec.Command("osascript", "-e", script).Start()
	default:
		// Linux：常见终端模拟器
		for _, t := range []string{"gnome-terminal", "x-terminal-emulator", "konsole", "xterm"} {
			if p, err := exec.LookPath(t); err == nil {
				return exec.Command(p, "--working-directory", s.Dir, "--", exe, "-run", s.ID).Start()
			}
		}
		return fmt.Errorf("找不到终端模拟器")
	}
}

// shellQuote 给 AppleScript 用的双引号包裹（转义内部引号）
func shellQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
