package main

// 回归测试：favorites.json（别名 / 软删除）数据链路。
// 注意：测试二进制运行在 go test 的临时目录，favPath() 指向那里，
// 不会污染真实 exe 旁的 favorites.json。

import (
	"encoding/json"
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
