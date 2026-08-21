//go:build windows && integration

package agent

// 验证：CREATE_NO_WINDOW 静默执行 claude agents --json 的调用链正常。
// （真正"不闪窗"由 SysProcAttr 保证，此处验证命令能执行且解析正常）

import (
	"context"
	"testing"
)

func TestFetchAgents(t *testing.T) {
	list, _ := FetchFull(context.Background())
	if list == nil {
		t.Fatal("FetchFull 返回 nil（命令执行失败或无法解析）")
	}
	t.Logf("agents 数量=%d", len(list))
	for _, a := range list {
		if a.SessionID == "" {
			t.Fatalf("条目缺少 sessionId: %+v", a)
		}
	}
}
