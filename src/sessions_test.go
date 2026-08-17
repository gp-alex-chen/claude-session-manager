package main

// parseSession 会话名解析测试：验证 /rename（custom-title）优先于 ai-title，
// 覆盖“改名在中间 / 未改名 / 改名在文件头 / 新会话尚无 ai-title”四种情况。

import (
	"os"
	"path/filepath"
	"testing"
)

// writeLines 把若干行写入临时 jsonl 文件，返回路径
func writeLines(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.jsonl")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

const (
	// 与 claude 2.1.x 真实格式一致：message 是 {role,content} 对象，content 为纯字符串
	lineUser   = `{"type":"user","message":{"role":"user","content":"hello world"},"cwd":"D:\\test","sessionId":"t1"}`
	lineAi     = `{"type":"ai-title","aiTitle":"修复bug并查看对应版本代码","sessionId":"t1"}`
	lineCustom = `{"type":"custom-title","customTitle":"android bug fix","sessionId":"t1"}`
	lineAgent  = `{"type":"agent-name","agentName":"android bug fix","sessionId":"t1"}`
)

// 真实场景：ai-title 先写入，会话中途 /rename 写入 custom-title + agent-name
func TestParseSessionRenameAfterAiTitle(t *testing.T) {
	p := writeLines(t,
		`{"type":"mode","mode":"normal","sessionId":"t1"}`,
		lineUser,
		lineAi,
		`{"type":"last-prompt","lastPrompt":"x","leafUuid":"u","sessionId":"t1"}`,
		`{"type":"system","subtype":"local_command","content":"/rename","sessionId":"t1"}`,
		lineCustom,
		lineAgent,
	)
	s := parseSession(p, "D:\\test")
	if s.Name != "android bug fix" {
		t.Errorf("Name = %q, want %q", s.Name, "android bug fix")
	}
	if s.Text != "hello world" {
		t.Errorf("Text = %q, want %q", s.Text, "hello world")
	}
	if s.Dir != "D:\\test" {
		t.Errorf("Dir = %q, want %q", s.Dir, "D:\\test")
	}
}

// 未改名：回退到 ai-title
func TestParseSessionNoRename(t *testing.T) {
	p := writeLines(t, lineUser, lineAi)
	s := parseSession(p, "")
	if s.Name != "修复bug并查看对应版本代码" {
		t.Errorf("Name = %q, want ai-title", s.Name)
	}
}

// 会话创建时就带自定义名（custom-title 在第 1 行，claude 重写文件时会出现）
func TestParseSessionRenameAtTop(t *testing.T) {
	p := writeLines(t, lineCustom, lineUser, lineAi)
	s := parseSession(p, "")
	if s.Name != "android bug fix" {
		t.Errorf("Name = %q, want %q", s.Name, "android bug fix")
	}
}

// 新会话：用户已改名但 ai-title 尚未生成
func TestParseSessionRenameNoAiTitle(t *testing.T) {
	p := writeLines(t, lineCustom, lineUser)
	s := parseSession(p, "")
	if s.Name != "android bug fix" {
		t.Errorf("Name = %q, want %q", s.Name, "android bug fix")
	}
}

// 系统注入型 user 消息：content 为 blocks 数组（如请求被打断）
func TestParseSessionBlocksContent(t *testing.T) {
	p := writeLines(t,
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]},"sessionId":"t1"}`,
		lineAi,
	)
	s := parseSession(p, "")
	if s.Text != "[Request interrupted by user for tool use]" {
		t.Errorf("Text = %q, want interrupted text", s.Text)
	}
}
