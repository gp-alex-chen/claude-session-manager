package updater

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestCheckIncludesReleaseBodyInInfo(t *testing.T) {
	const notes = "修复更新提示\n优化下载进度显示"
	u := New("gp-alex-chen", "claude-session-manager", "claude-terminal.exe", "v0.1-wails")
	u.Client = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Status:        "200 OK",
				Body:          io.NopCloser(strings.NewReader(`[{"tag_name":"v0.2-wails","body":"修复更新提示\n优化下载进度显示","assets":[{"name":"claude-terminal.exe","state":"uploaded","digest":"sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"}]}]`)),
				Header:        make(http.Header),
				ContentLength: -1,
				Request:       req,
			}, nil
		}),
	}

	info, err := u.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !info.HasUpdate || info.LatestNotes != notes {
		t.Fatalf("check info did not preserve release notes: %#v", info)
	}
}

func TestCheckCarriesPublishedAssetDigest(t *testing.T) {
	const digest = "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	u := New("gp-alex-chen", "claude-session-manager", "claude-terminal.exe", "v0.1-wails")
	u.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `[{"tag_name":"v0.2-wails","assets":[{"name":"claude-terminal.exe","state":"uploaded","digest":"` + digest + `"}]}]`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: req}, nil
	})}
	info, err := u.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.SHA256 != strings.TrimPrefix(digest, "sha256:") {
		t.Fatalf("SHA256 = %q, want published digest", info.SHA256)
	}
}

func TestDownloadRejectsUnknownLengthBeyondLimit(t *testing.T) {
	u := New("gp-alex-chen", "claude-session-manager", "claude-terminal.exe", "dev")
	u.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("MZoversized")), ContentLength: -1, Header: make(http.Header), Request: req}, nil
	})}
	dest := filepath.Join(t.TempDir(), "update.exe")
	err := u.downloadTo(context.Background(), &Info{URL: "https://example.test/update.exe", SHA256: strings.Repeat("0", 64)}, dest, nil, 4)
	if err == nil || !strings.Contains(err.Error(), "过大") {
		t.Fatalf("unknown-length response exceeded byte limit: %v", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("temporary download remains after limit error: %v", statErr)
	}
}

func TestDownloadRejectsDigestMismatch(t *testing.T) {
	u := New("gp-alex-chen", "claude-session-manager", "claude-terminal.exe", "dev")
	u.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("MZwrong")), ContentLength: 7, Header: make(http.Header), Request: req}, nil
	})}
	dest := filepath.Join(t.TempDir(), "update.exe")
	info := &Info{URL: "https://example.test/update.exe", SHA256: strings.Repeat("0", 64)}
	completed := false
	if err := u.DownloadTo(context.Background(), info, dest, func(percent int, _, _ int64) {
		if percent == 100 {
			completed = true
		}
	}); err == nil {
		t.Fatal("download with mismatched published digest was accepted")
	}
	if completed {
		t.Fatal("corrupt download reported 100% before verification")
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("temporary download remains after digest error: %v", statErr)
	}
}

func TestDownloadAcceptsMatchingPublishedDigest(t *testing.T) {
	const payload = "MZvalid"
	u := New("gp-alex-chen", "claude-session-manager", "claude-terminal.exe", "dev")
	u.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(payload)), ContentLength: int64(len(payload)), Header: make(http.Header), Request: req}, nil
	})}
	dest := filepath.Join(t.TempDir(), "update.exe")
	digest := sha256.Sum256([]byte(payload))
	info := &Info{URL: "https://example.test/update.exe", SHA256: fmt.Sprintf("%x", digest)}
	if err := u.DownloadTo(context.Background(), info, dest, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != payload {
		t.Fatalf("download = %q, err = %v", got, err)
	}
}

func TestReplaceAndStartRestoresOldExecutableOnLaunchFailure(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "claude-terminal.exe")
	downloaded := self + ".new"
	if err := os.WriteFile(self, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(downloaded, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := replaceAndStart(self, downloaded, func(string) error { return errors.New("launch failed") })
	if err == nil {
		t.Fatal("expected launch failure")
	}
	oldBytes, err := os.ReadFile(self)
	if err != nil || string(oldBytes) != "old" {
		t.Fatalf("old executable was not restored: %q, %v", oldBytes, err)
	}
	newBytes, err := os.ReadFile(downloaded)
	if err != nil || string(newBytes) != "new" {
		t.Fatalf("new executable was not staged for retry: %q, %v", newBytes, err)
	}
}

func TestReplaceAndStartUsesUniqueOldExecutableNames(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "claude-terminal.exe")
	downloaded := self + ".new"
	backups := []string{}
	for _, content := range []string{"first", "second"} {
		if err := os.WriteFile(self, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(downloaded, []byte("next"), 0o644); err != nil {
			t.Fatal(err)
		}
		backup, err := replaceAndStart(self, downloaded, func(string) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(backup)
		if err != nil || string(got) != content {
			t.Fatalf("backup = %q, err = %v", got, err)
		}
		backups = append(backups, backup)
	}
	if backups[0] == backups[1] {
		t.Fatalf("backup path was reused: %q", backups[0])
	}
	first, err := os.ReadFile(backups[0])
	if err != nil || string(first) != "first" {
		t.Fatalf("earlier backup was overwritten: %q, %v", first, err)
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
