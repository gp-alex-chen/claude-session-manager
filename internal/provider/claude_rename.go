package provider

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

//go:embed claude_rename_helper.cjs
var claudeRenameHelper []byte

// RenameClaude calls the bundled official SDK's offline session mutation API.
// Script and arguments travel separately; no shell or model query is started.
func RenameClaude(parent context.Context, lookPath func(string) (string, error), id, name, dir string) error {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	node, err := lookPath("node")
	if err != nil {
		return fmt.Errorf("Claude 原生重命名需要 Node.js 18 或更新版本，请安装并加入 PATH: %w", err)
	}
	payload, err := json.Marshal(map[string]string{"sessionId": id, "title": name, "dir": dir})
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, node, "-", string(payload))
	configureRPCProcess(cmd)
	cmd.Stdin = bytes.NewReader(claudeRenameHelper)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("Claude 重命名超时或取消: %w", ctx.Err())
		}
		message := strings.TrimSpace(stderr.String())
		if len(message) > 2048 {
			message = message[:2048]
		}
		return fmt.Errorf("Claude SDK 重命名失败: %v %s", err, message)
	}
	var result struct {
		SessionID string `json:"sessionId"`
		Title     string `json:"title"`
		Renamed   bool   `json:"renamed"`
	}
	if json.Unmarshal(stdout.Bytes(), &result) != nil || !result.Renamed || result.SessionID != id || result.Title != name {
		return fmt.Errorf("Claude SDK 未确认目标会话的重命名结果")
	}
	return nil
}
