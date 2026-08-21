package updater

import (
	"path/filepath"
	"testing"
)

// —— 版本/筛选逻辑（纯函数，不联网） ——

func TestPickLatest(t *testing.T) {
	cases := []struct {
		name        string
		list        []Release
		includePre  bool
		want        string // 期望的最佳 tag；"" 表示期望没有合适项
		wantHasBest bool
	}{
		{
			name: "混合仓库tag只取wails正式版",
			list: []Release{
				{Tag: "v0.1-beta"},                 // fyne 版，无 -wails
				{Tag: "v0.2"},                      // fyne 版
				{Tag: "v0.1-wails"},                // wails 正式版
				{Tag: "v0.2-wails"},                // wails 正式版（最新稳定）
				{Tag: "v0.3-wails-pre", Pre: true}, // wails 预发布（CI 标 prerelease=true）
				{Tag: "v0.3-wails-rc", Pre: true},  // wails 预发布
			},
			want:        "v0.2-wails",
			wantHasBest: true,
		},
		{
			name: "全部非wails返回无",
			list: []Release{
				{Tag: "v0.1"},
				{Tag: "v0.2-beta"},
			},
			want:        "",
			wantHasBest: false,
		},
		{
			name: "非法语义版本跳过",
			list: []Release{
				{Tag: "latest-wails"}, // 非合法语义版本
				{Tag: "v1.0-wails"},
			},
			want:        "v1.0-wails",
			wantHasBest: true,
		},
		{
			name:        "空列表",
			list:        []Release{},
			want:        "",
			wantHasBest: false,
		},
	}
	for _, c := range cases {
		got, ok := pickLatest(c.list, c.includePre)
		if ok != c.wantHasBest {
			t.Fatalf("%s: ok=%v want %v", c.name, ok, c.wantHasBest)
		}
		if c.wantHasBest && got.Tag != c.want {
			t.Fatalf("%s: got %q want %q", c.name, got.Tag, c.want)
		}
	}
}

func TestPickLatestIncludePre(t *testing.T) {
	// 显式测试：includePrerelease=true 时，版本号更高的预发布被选中
	list := []Release{
		{Tag: "v0.1-wails"},
		{Tag: "v0.3-wails-rc", Pre: true},
		{Tag: "v0.2-wails"},
	}
	got, ok := pickLatest(list, true)
	if !ok || got.Tag != "v0.3-wails-rc" {
		t.Fatalf("includePre: got %q ok=%v, want v0.3-wails-rc", got.Tag, ok)
	}
	// 默认不含预发布时退回最高正式版
	got, ok = pickLatest(list, false)
	if !ok || got.Tag != "v0.2-wails" {
		t.Fatalf("!includePre: got %q ok=%v, want v0.2-wails", got.Tag, ok)
	}
}

func TestIsWailsTag(t *testing.T) {
	for tag, want := range map[string]bool{
		"v0.1-wails":     true,
		"v0.2.3-wails":   true,
		"v0.1":           false,
		"v0.2-wails-pre": true, // 仍属于 wails 前缀（是否可用由 prerelease 标志决定）
		"wails":          false,
		"":               false,
	} {
		if got := isWailsTag(tag); got != want {
			t.Errorf("isWailsTag(%q)=%v want %v", tag, got, want)
		}
	}
}

func TestCompareToCurrent(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.1-wails", "v0.2-wails", true},
		{"v0.2-wails", "v0.2-wails", false},
		{"v0.3-wails", "v0.2-wails", false},
		{"dev", "v0.2-wails", true}, // 非语义版本一律提示可更新
		{"", "v0.2-wails", true},
	}
	for _, c := range cases {
		if got := compareToCurrent(c.current, c.latest); got != c.want {
			t.Errorf("compareToCurrent(%q,%q)=%v want %v", c.current, c.latest, got, c.want)
		}
	}
}

// —— 其它零依赖小逻辑 ——

func TestIsPEExecutable(t *testing.T) {
	// 拿本测试文件自身当作"非 PE"
	if isPEExecutable(filepath.Join("updater_test.go")) {
		t.Error("文本文件不应被判为 PE")
	}
}

// TestNoneUncovered 仅确保直链拼装格式符合预期（顺带覆盖 downloadFmt）。
func TestDownloadURLFormat(t *testing.T) {
	u := New("gp-alex-chen", "claude-session-manager", "claude-terminal.exe", "v0.1-wails")
	want := "https://github.com/gp-alex-chen/claude-session-manager/releases/download/v0.2-wails/claude-terminal.exe"
	got := u.downloadURL("v0.2-wails")
	if got != want {
		t.Fatalf("downloadURL: got %q want %q", got, want)
	}
}
