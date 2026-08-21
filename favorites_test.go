package main

// 回归测试：favorites.json（别名 / 软删除）数据链路。
// 注意：测试二进制运行在 go test 的临时目录，favPath() 指向那里，
// 不会污染真实 exe 旁的 favorites.json。

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"claude-terminal/internal/favorites"
)

func TestFavAliasAndDelete(t *testing.T) {
	os.Remove(favorites.FavPath()) // 清掉上次测试残留
	a := &App{}

	// 从真实扫描结果取一个会话 id 作为操作对象
	list := a.ListSessions()
	if len(list) == 0 {
		t.Skip("本机无 claude 会话，跳过")
	}
	id := list[0].ID
	orig := list[0].Name

	// 1) 重命名：设置别名
	if err := a.RenameSession(id, "测试别名"); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	st := favorites.Load()
	if st.Aliases[id] != "测试别名" {
		t.Fatalf("别名未写入: %q", st.Aliases[id])
	}
	// 列表中该会话名字应为别名
	after := a.ListSessions()
	found := false
	for _, s := range after {
		if s.ID == id {
			found = true
			if s.Name != "测试别名" {
				t.Fatalf("列表名应为别名，实际 %q", s.Name)
			}
		}
	}
	if !found {
		t.Fatal("重命名后会话应仍在列表中")
	}

	// 2) 重命名：空串清除别名，恢复原名
	if err := a.RenameSession(id, "   "); err != nil {
		t.Fatalf("RenameSession(空): %v", err)
	}
	st = favorites.Load()
	if _, ok := st.Aliases[id]; ok {
		t.Fatal("别名应被清除")
	}
	after = a.ListSessions()
	for _, s := range after {
		if s.ID == id && s.Name != orig {
			t.Fatalf("清除别名后应恢复原名，实际 %q", s.Name)
		}
	}

	// 3) 删除：软隐藏
	if err := a.DeleteSession(id); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	for _, s := range a.ListSessions() {
		if s.ID == id {
			t.Fatal("删除后列表不应再包含该会话")
		}
	}
	hidden := a.ListHiddenSessions()
	found = false
	for _, s := range hidden {
		if s.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatal("已删列表应包含该会话")
	}

	// 4) 恢复：Unhide
	if err := a.UnhideSession(id); err != nil {
		t.Fatalf("UnhideSession: %v", err)
	}
	for _, s := range a.ListSessions() {
		if s.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatal("恢复后列表应重新包含该会话")
	}

	// 5) 文件格式与 fyne-sidebar 兼容：可再次解析出三键
	b, err := os.ReadFile(favorites.FavPath())
	if err != nil {
		t.Fatalf("favorites.json 未写出: %v", err)
	}
	var j struct {
		Ids     []string          `json:"ids"`
		Aliases map[string]string `json:"aliases"`
		Hidden  []string          `json:"hidden"`
	}
	if err := json.Unmarshal(b, &j); err != nil {
		t.Fatalf("favorites.json 格式解析失败: %v", err)
	}
	if j.Aliases == nil || j.Hidden == nil {
		t.Fatal("favorites.json 应含 aliases 与 hidden 字段")
	}

	os.Remove(favorites.FavPath()) // 清理测试残留
	t.Log("favorites 数据链路 OK")
}

// TestOpenSessionsRoundtrip 打开的会话集合：new- 临时 token 不记录、按序稳定。
func TestOpenSessionsRoundtrip(t *testing.T) {
	os.Remove(favorites.OpenPath())
	a := &App{}

	if got := a.GetOpenSessions(); len(got) != 0 {
		t.Fatal("初始应为空集合")
	}
	// 模拟三个"存活终端"：两个真实会话 + 一个 new- 临时会话
	a.terms = map[string]*ptyRef{"session-b": {}, "new-xyz": {}, "session-a": {}}
	a.persistOpenSessions()
	got := a.GetOpenSessions()
	want := []string{"session-a", "session-b"} // new- 排除，按序排列
	if len(got) != len(want) {
		t.Fatalf("应恢复 %v，实际 %v", want, got)
	}
	for i, id := range want {
		if got[i] != id {
			t.Fatalf("集合不对：期望 %v，实际 %v", want, got)
		}
	}

	os.Remove(favorites.OpenPath()) // 清理测试残留
	t.Log("open-sessions 数据链路 OK")
}

// TestShellChoice 底层 Shell 选择：命令行组装 / 选型校验 / 启动兜底。
// 用 lookPath 桩模拟 pwsh 可用与缺失，不依赖测试机是否装 pwsh。
func TestShellChoice(t *testing.T) {
	os.Remove(favorites.ShellPath())
	defer os.Remove(favorites.ShellPath())
	a := &App{}
	old := lookPath
	defer func() { lookPath = old }()
	missing := func(name string) (string, error) { return "", errors.New("not found") }

	// 默认 cmd；cmd 恒可用，命令行不受机器环境影响
	if got := a.GetShell(); got != "cmd" {
		t.Fatalf("默认应为 cmd，实际 %q", got)
	}
	if got := a.claudeCmd("-r abc"); got != "cmd /c claude -r abc" {
		t.Fatalf("cmd 恢复命令行不对: %q", got)
	}
	if got := a.claudeCmd(""); got != "cmd /c claude" {
		t.Fatalf("cmd 新建命令行不对: %q", got)
	}

	// 模拟 pwsh 可用：正常切换，命令行用 pwsh
	lookPath = func(name string) (string, error) {
		if name == "pwsh" {
			return `C:\Program Files\PowerShell\7\pwsh.exe`, nil
		}
		return missing(name)
	}
	if !a.shellAvailable("pwsh") {
		t.Fatal("模拟 pwsh 可用时应返回 true")
	}
	if err := a.SetShell("pwsh"); err != nil {
		t.Fatalf("pwsh 可用时 SetShell 不应报错: %v", err)
	}
	if got := a.claudeCmd("-r abc"); got != `pwsh -NoLogo -NoExit -Command "claude -r abc"` {
		t.Fatalf("pwsh 恢复命令行不对: %q", got)
	}
	if got := a.claudeCmd(""); got != `pwsh -NoLogo -NoExit -Command "claude "` {
		t.Fatalf("pwsh 新建命令行不对: %q", got)
	}

	// 模拟 pwsh 缺失：SetShell 拒绝；claudeCmd 兜底回退 cmd（设置仍保留 pwsh）
	lookPath = missing
	if err := a.SetShell("pwsh"); err == nil {
		t.Fatal("pwsh 缺失时 SetShell 应报错")
	}
	if got := a.GetShell(); got != "pwsh" {
		t.Fatalf("用户偏好应保留 pwsh，实际 %q", got)
	}
	if got := a.claudeCmd("-r abc"); got != "cmd /c claude -r abc" {
		t.Fatalf("pwsh 缺失应兜底回退 cmd，实际 %q", got)
	}

	// 非法值 SetShell 仍回退 cmd 语义
	lookPath = missing
	if err := a.SetShell("bash"); err != nil {
		t.Fatalf("非法 Shell 不应报错: %v", err)
	}
	if got := a.GetShell(); got != "cmd" {
		t.Fatalf("非法 Shell 应回退 cmd，实际 %q", got)
	}
	t.Log("shell 选择链路 OK")
}
