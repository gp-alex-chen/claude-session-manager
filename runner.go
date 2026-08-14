package main

// -run <会话ID> 恢复模式：在终端标签页里直接恢复会话（单二进制，零依赖，
// 不再需要 resume-session.ps1 / pwsh）。由 openSession 以 `claude-sidebar -run <id>` 启动。

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// AgentInfo claude agents --json 的条目
type AgentInfo struct {
	SessionID string `json:"sessionId"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	State     string `json:"state"`
}

func fetchAgentsFull() []AgentInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/c", "claude", "agents", "--json")
	} else {
		cmd = exec.CommandContext(ctx, "claude", "agents", "--json")
	}
	cmd.Env = cleanEnv()
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var list []AgentInfo
	if json.Unmarshal(out, &list) != nil {
		return nil
	}
	return list
}

// cleanEnv 去掉可能继承的 NO_COLOR（claude 会因此关闭颜色）
func cleanEnv() []string {
	env := []string{}
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "NO_COLOR=") {
			env = append(env, e)
		}
	}
	return env
}

// runClaude 以子进程运行 claude（继承本进程的终端）
func runClaude(args []string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// claude 是 npm shim（.cmd），用 cmd /c 确保解析
		all := append([]string{"/c", "claude"}, args...)
		cmd = exec.Command("cmd", all...)
	} else {
		cmd = exec.Command("claude", args...)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = cleanEnv()
	return cmd.Run()
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func findSessionFile(id string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, ".claude", "projects")
	projDirs, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	for _, pd := range projDirs {
		if !pd.IsDir() {
			continue
		}
		p := filepath.Join(root, pd.Name(), id+".jsonl")
		if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("找不到会话文件: %s", id)
}

// waitEnter 让标签页保持打开（claude 退出后不立即消失）
func waitEnter() {
	fmt.Println("按回车关闭标签页…")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

// runSession 恢复模式主流程（dry=true 只检查不实际执行 claude，调试用）
func runSession(id string, dry bool) error {
	os.Unsetenv("NO_COLOR")

	path, err := findSessionFile(id)
	if err != nil {
		fmt.Println(err)
		waitEnter()
		return nil
	}

	s := parseSession(path, "")
	dir := s.Dir
	if dir == "" || !pathExists(dir) {
		// 兜底：从项目目录名解码（冒号/反斜杠都被编码为 -）
		dir = decodeDir(filepath.Base(filepath.Dir(path)))
	}
	if !pathExists(dir) {
		fmt.Printf("警告: 找不到会话目录 [%s]，留在当前目录（会话可能无法恢复）\n", dir)
	} else if err := os.Chdir(dir); err != nil {
		fmt.Printf("警告: 无法进入目录 [%s]: %v\n", dir, err)
	}

	// 检查活动状态（已打开的交互会话 / 运行中的后台代理）——不重复打开
	for _, a := range fetchAgentsFull() {
		if a.SessionID != id {
			continue
		}
		if a.Kind == "background" {
			fmt.Printf("该会话正在后台运行中: %s（状态: %s），打开代理视图 attach…\n", a.Name, a.State)
			if dry {
				fmt.Println("DRY: 将执行 claude agents")
				return nil
			}
			if err := runClaude([]string{"agents"}); err != nil {
				fmt.Printf("claude agents 退出: %v\n", err)
			}
			return nil
		}
		fmt.Printf("该会话已在你另一个标签页中打开: %s，无需重复恢复。\n", a.Name)
		fmt.Println("标签页将保持打开，可关闭或输入其他命令。")
		return nil
	}

	fmt.Printf("恢复会话: %s  (claude -r %s)\n", dir, id)
	if dry {
		fmt.Println("DRY: 已通过全部检查（会话文件/目录/活动状态），未实际执行 claude。")
		return nil
	}
	if err := runClaude([]string{"-r", id}); err != nil {
		fmt.Printf("claude 已退出（错误: %v）。\n", err)
	} else {
		fmt.Println("claude 已退出。")
	}
	waitEnter()
	return nil
}
