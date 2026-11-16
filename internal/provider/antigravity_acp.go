package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// AntigravityACP only discovers public metadata. It never creates, loads,
// prompts, authenticates or mutates a session, and grants no client tools.
type AntigravityACP struct {
	Open func(context.Context) (io.ReadWriteCloser, error)
}

func antigravityACPForExecutable(bin string) *AntigravityACP {
	return &AntigravityACP{Open: func(ctx context.Context) (io.ReadWriteCloser, error) { return openAntigravityACPProcess(ctx, bin) }}
}

func openAntigravityACPProcess(ctx context.Context, bin string) (io.ReadWriteCloser, error) {
	cmd := exec.CommandContext(ctx, bin, "acp", "--stdio")
	cmd.Env = append(os.Environ(), antigravityDisableAutoUpdateEnv)
	configureRPCProcess(cmd)
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, err
	}
	return &processConnection{Reader: stdout, Writer: stdin, cmd: cmd}, nil
}

func (c *AntigravityACP) ListSessions(parent context.Context) ([]Session, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, antigravityHistoryTimeout)
	defer cancel()
	conn, err := c.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	r := newRPCSession(ctx, conn)
	r.protocol = "Antigravity ACP"
	r.jsonRPC = true
	if err := initializeAntigravityACP(ctx, r); err != nil {
		return nil, err
	}
	return listAntigravityACPSessions(ctx, r)
}

func initializeAntigravityACP(ctx context.Context, r *rpcSession) error {
	var init struct {
		ProtocolVersion   int `json:"protocolVersion"`
		AgentCapabilities struct {
			SessionCapabilities struct {
				List json.RawMessage `json:"list"`
			} `json:"sessionCapabilities"`
		} `json:"agentCapabilities"`
	}
	if err := r.callContext(ctx, "initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}, "clientInfo": map[string]string{"name": "TheOne", "version": "1"}}, &init); err != nil {
		return err
	}
	if init.ProtocolVersion != 1 || len(init.AgentCapabilities.SessionCapabilities.List) == 0 || string(init.AgentCapabilities.SessionCapabilities.List) == "null" {
		return errors.New("Antigravity CLI 未提供 ACP v1 会话列表能力")
	}
	var capability map[string]json.RawMessage
	if err := json.Unmarshal(init.AgentCapabilities.SessionCapabilities.List, &capability); err != nil || capability == nil {
		return errors.New("Antigravity ACP 会话列表能力格式无效")
	}
	return nil
}

func listAntigravityACPSessions(ctx context.Context, r *rpcSession) ([]Session, error) {
	out := []Session{}
	cursor := ""
	cursors := map[string]bool{}
	ids := map[string]string{}
	rows := 0
	for page := 0; page < 1000; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var response struct {
			Sessions *[]struct {
				SessionID             string `json:"sessionId"`
				Cwd, Title, UpdatedAt string
			} `json:"sessions"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := r.callContext(ctx, "session/list", params, &response); err != nil {
			return nil, err
		}
		if response.Sessions == nil {
			return nil, errors.New("Antigravity ACP 返回缺少 sessions 数组")
		}
		rows += len(*response.Sessions)
		if rows > 100000 {
			return nil, errors.New("Antigravity ACP 会话记录数量超过上限")
		}
		for _, item := range *response.Sessions {
			if !uuidPattern.MatchString(item.SessionID) {
				continue
			}
			dir, err := normalizeAntigravityDirectory(item.Cwd)
			if err != nil {
				continue
			}
			if previous, ok := ids[item.SessionID]; ok {
				if !strings.EqualFold(previous, dir) {
					return nil, errors.New("Antigravity ACP 同一会话返回了冲突目录")
				}
				continue
			}
			// updatedAt is optional display metadata, not identity evidence.
			updated, _ := time.Parse(time.RFC3339Nano, item.UpdatedAt)
			ids[item.SessionID] = dir
			name := antigravityTitle(item.Title)
			if name == "" {
				name = "Antigravity · " + item.SessionID[:8]
			}
			out = append(out, Session{Ref: SessionRef{Antigravity, item.SessionID}, Dir: dir, Name: name, Time: updated})
			if len(out) > 100000 {
				return nil, errors.New("Antigravity ACP 会话数量超过上限")
			}
		}
		if response.NextCursor == nil || *response.NextCursor == "" {
			return out, nil
		}
		cursor = *response.NextCursor
		if cursors[cursor] {
			return nil, errors.New("Antigravity ACP 返回了重复分页标记")
		}
		cursors[cursor] = true
	}
	return nil, errors.New("Antigravity ACP 分页超过上限")
}

func normalizeAntigravityDirectory(raw string) (string, error) {
	if strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", errors.New("invalid Antigravity directory")
	}
	path := strings.TrimSpace(raw)
	if len(path) >= 4 && path[0] == '/' && path[2] == ':' && (path[3] == '/' || path[3] == '\\') && ((path[1] >= 'a' && path[1] <= 'z') || (path[1] >= 'A' && path[1] <= 'Z')) {
		path = path[1:]
	}
	if strings.HasPrefix(path, "///") || strings.HasPrefix(path, `\\\`) {
		return "", errors.New("invalid Antigravity directory prefix")
	}
	path = filepath.Clean(filepath.FromSlash(path))
	if !filepath.IsAbs(path) || filepath.VolumeName(path) == "" {
		return "", errors.New("Antigravity cwd is not an absolute Windows path")
	}
	return path, nil
}

func antigravityTitle(value string) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value))
	runes := []rune(value)
	if len(runes) > 120 {
		value = string(runes[:120])
	}
	return value
}
