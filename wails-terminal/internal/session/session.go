// Package session 会话数据层：解析 ~/.claude/projects/**/*.jsonl
// （与 PowerShell 版同源逻辑）
package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	// reCustomTitle：用户 /rename 的自定义会话名（Claude Code 2.1+ 新增类型）
	reCustomTitle = regexp.MustCompile(`"customTitle":"([^"]*)"`)
	// reSessionName：AI 生成的标题（ai-title）与旧版改名字段（agent-name）
	reSessionName = regexp.MustCompile(`"aiTitle":"([^"]*)"|"agentName":"([^"]*)"`)
	reProj        = regexp.MustCompile(`^([A-Za-z])--(.+)$`)
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
	var aiTitle, custom string
	for sc.Scan() {
		n++
		if n > 5000 || (s.Text != "" && aiTitle != "" && custom != "") {
			break
		}
		raw := sc.Bytes()
		// 会话名三来源，优先级：custom-title（用户 /rename）> ai-title/agent-name。
		// 这些行会被 claude 周期性重写（可能在文件头、也可能在改名时刻），
		// 因此继续扫描并保留最后一次看到的值，避免被早期的旧标题锁死。
		if bytes.Contains(raw, []byte(`"type":"custom-title"`)) {
			if m := reCustomTitle.FindSubmatch(raw); m != nil {
				custom = string(m[1])
			}
		}
		if bytes.Contains(raw, []byte(`"type":"ai-title"`)) || bytes.Contains(raw, []byte(`"type":"agent-name"`)) {
			if m := reSessionName.FindSubmatch(raw); m != nil {
				if len(m[1]) > 0 {
					aiTitle = string(m[1])
				} else {
					aiTitle = string(m[2])
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
			s.Text = messageText(l.Message)
		}
	}
	if custom != "" {
		s.Name = custom // 用户 /rename 的名字优先
	} else {
		s.Name = aiTitle
	}
	return s
}

// messageText 从 user 消息的 message 字段提取可显示文本。claude 不同版本格式不同：
//  1) "message":"纯文本"（早期格式）
//  2) "message":{"role":"user","content":"纯文本"}（2.1.x 主流格式）
//  3) content 为 blocks 数组（如系统注入的打断/通知消息）
//  4) "message":[{"type":"text","text":"…"}]（兼容其他格式）
func messageText(msg json.RawMessage) string {
	var str string
	if json.Unmarshal(msg, &str) == nil && str != "" {
		return str
	}
	var obj struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(msg, &obj) == nil && len(obj.Content) > 0 {
		if json.Unmarshal(obj.Content, &str) == nil && str != "" {
			return str
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(obj.Content, &blocks) == nil {
			for _, b := range blocks {
				if b.Type == "text" && b.Text != "" {
					return b.Text
				}
			}
		}
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(msg, &blocks) == nil {
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				return b.Text
			}
		}
	}
	return ""
}

func ScanAll() []*Session {
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
func DisplayName(s *Session) string {
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

// leafLabel 项目组显示名：默认只显示末端目录名（项目名）；存在同名目录时
// 追加父目录名消歧，避免 C:\a\app 与 D:\b\app 都显示成 "app"。
// 完整路径在界面悬浮提示中展示。
func leafLabel(dir string, count map[string]int) string {
	leaf := filepath.Base(dir)
	if count[leaf] > 1 {
		parent := filepath.Base(filepath.Dir(dir))
		if parent != "" && parent != "." && parent != leaf {
			return leaf + " · " + parent
		}
	}
	return leaf
}

