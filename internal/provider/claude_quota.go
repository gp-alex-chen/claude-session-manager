package provider

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

//go:embed claude_quota_helper.cjs
var claudeQuotaHelper []byte

func ResolveClaudeExecutable(lookPath func(string) (string, error)) (string, error) {
	path, err := lookPath("claude")
	if err != nil {
		return "", errors.New("未检测到 Claude CLI")
	}
	if strings.EqualFold(filepath.Ext(path), ".exe") {
		return path, nil
	}
	arch := "x64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	root := filepath.Join(filepath.Dir(path), "node_modules", "@anthropic-ai", "claude-code")
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	for _, candidate := range []string{filepath.Join(root, "bin", "claude.exe"),
		filepath.Join(root, "node_modules", "@anthropic-ai", "claude-code-win32-"+arch, "claude.exe"),
		filepath.Join(filepath.Dir(root), "claude-code-win32-"+arch, "claude.exe")} {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}
	}
	return "", errors.New("额度查询需要原生 Claude CLI，请更新官方安装")
}

// Only the SDK's get_usage control is called. No prompt, tools, project hooks,
// transcript persistence, private OAuth file reads, or model requests.
func ReadClaudeQuota(parent context.Context, lookPath func(string) (string, error)) (QuotaSnapshot, error) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	cli, err := ResolveClaudeExecutable(lookPath)
	if err != nil {
		return QuotaSnapshot{}, err
	}
	node, err := lookPath("node")
	if err != nil {
		return QuotaSnapshot{}, errors.New("Claude 额度查询需要 Node.js 18 或更新版本")
	}
	return runClaudeQuotaHelper(ctx, node, cli, nil)
}

func runClaudeQuotaHelper(ctx context.Context, node, cli string, env []string) (QuotaSnapshot, error) {
	dir, err := os.MkdirTemp("", "claude-quota-")
	if err != nil {
		return QuotaSnapshot{}, err
	}
	defer os.RemoveAll(dir) // Only this invocation's newly created temporary directory.
	helper := filepath.Join(dir, "quota.cjs")
	if err = os.WriteFile(helper, claudeQuotaHelper, 0600); err != nil {
		return QuotaSnapshot{}, err
	}
	payload, _ := json.Marshal(map[string]string{"executable": cli, "cwd": dir})
	cmd := exec.CommandContext(ctx, node, helper, string(payload))
	cmd.Env = env
	configureRPCProcess(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return QuotaSnapshot{}, err
	}
	defer stdin.Close()
	// EOF lets the helper abort and close its SDK child before Go's hard deadline.
	cmd.Cancel = func() error { return stdin.Close() }
	cmd.WaitDelay = 4 * time.Second
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	job, err := newQuotaProcessJob()
	if err != nil {
		return QuotaSnapshot{}, err
	}
	defer job.close()
	if err = cmd.Start(); err != nil {
		return QuotaSnapshot{}, err
	}
	// The helper cannot spawn Claude until assigned to the cleanup job.
	if err = job.attach(cmd); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return QuotaSnapshot{}, err
	}
	if _, err = stdin.Write([]byte("start\n")); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return QuotaSnapshot{}, err
	}
	if err = cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return QuotaSnapshot{}, ctx.Err()
		}
		return QuotaSnapshot{}, errors.New("Claude 原生额度接口不可用，请检查 CLI 版本和登录状态")
	}
	return parseClaudeQuota(stdout.Bytes())
}

func parseClaudeQuota(data []byte) (QuotaSnapshot, error) {
	var raw struct {
		AuthMode  string                     `json:"auth_mode"`
		Plan      string                     `json:"subscription_type"`
		Available *bool                      `json:"rate_limits_available"`
		Limits    map[string]json.RawMessage `json:"rate_limits"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw.Available == nil {
		return QuotaSnapshot{}, errors.New("无效的 Claude 额度响应")
	}
	q := QuotaSnapshot{Plan: raw.Plan, Status: "available", Windows: []QuotaWindow{}}
	if !*raw.Available {
		q.Status = "unsupported"
		q.Message = "当前登录方式不提供订阅额度"
		if raw.AuthMode == "api" && raw.Plan == "" {
			q.AuthMode = "api"
			q.Message = "当前使用 API 方式，自动额度查询已暂停；切换订阅登录后请手动刷新"
		}
		if raw.Plan != "" {
			q.Status = "error"
			q.Message = "订阅额度暂时无法读取"
		}
		return q, nil
	}
	q.AuthMode = "subscription"
	for _, spec := range []struct {
		id, label string
		minutes   int64
		scope     string
	}{
		{"five_hour", "5 小时", 300, ""}, {"seven_day", "周", 10080, ""},
		{"seven_day_opus", "Opus 周", 10080, "Opus"}, {"seven_day_sonnet", "Sonnet 周", 10080, "Sonnet"},
		{"seven_day_oauth_apps", "应用周", 10080, "OAuth 应用"},
	} {
		var w *struct {
			Used  *float64 `json:"utilization"`
			Reset string   `json:"resets_at"`
		}
		data := raw.Limits[spec.id]
		if len(data) == 0 {
			continue
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return q, errors.New("无效的 Claude 额度窗口")
		}
		if w == nil || w.Used == nil {
			continue
		}
		if math.IsNaN(*w.Used) || math.IsInf(*w.Used, 0) || *w.Used < 0 || *w.Used > 100 {
			return QuotaSnapshot{}, errors.New("无效的 Claude 额度百分比")
		}
		row := QuotaWindow{ID: spec.id, Label: spec.label, UsedPercent: *w.Used, WindowMinutes: spec.minutes, Scope: spec.scope}
		if reset, err := time.Parse(time.RFC3339, w.Reset); err == nil {
			row.ResetsAt = reset.Unix()
		}
		q.Windows = append(q.Windows, row)
	}
	var models []struct {
		Name  string   `json:"display_name"`
		Used  *float64 `json:"utilization"`
		Reset string   `json:"resets_at"`
	}
	if data := raw.Limits["model_scoped"]; len(data) > 0 && json.Unmarshal(data, &models) == nil {
		for i, w := range models {
			if w.Used == nil || *w.Used < 0 || *w.Used > 100 {
				continue
			}
			row := QuotaWindow{ID: fmt.Sprintf("model:%d", i), Label: w.Name + " 周", Scope: w.Name, UsedPercent: *w.Used, WindowMinutes: 10080}
			if reset, err := time.Parse(time.RFC3339, w.Reset); err == nil {
				row.ResetsAt = reset.Unix()
			}
			q.Windows = append(q.Windows, row)
		}
	}
	if len(q.Windows) == 0 {
		return q, fmt.Errorf("Claude 未返回可读取的订阅额度")
	}
	return q, nil
}
