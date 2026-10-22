package notify

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gp-alex-chen/claude-session-manager/internal/state"
)

const programErrorLimit = 4096

type limitedError struct{ data []byte }

func (b *limitedError) Write(p []byte) (int, error) {
	n := len(p)
	left := programErrorLimit - len(b.data)
	if left > len(p) {
		left = len(p)
	}
	if left > 0 {
		b.data = append(b.data, p[:left]...)
	}
	return n, nil
}
func runProgram(ctx context.Context, c state.NotificationConfig, payload []byte) ProgramResult {
	result := ProgramResult{ExitCode: -1}
	cmd := exec.CommandContext(ctx, c.Program, c.Args...)
	cmd.Dir = c.WorkingDir
	if cmd.Dir == "" {
		if path, err := os.Executable(); err == nil {
			cmd.Dir = filepath.Dir(path)
		}
	}
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Stdout = io.Discard
	stderr := &limitedError{}
	cmd.Stderr = stderr
	cmd.WaitDelay = time.Second
	configureProgramProcess(cmd)
	err := runProgramCommand(cmd)
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.Error = "通知程序执行超时"
		} else {
			result.Error = "通知程序调用已取消"
		}
	} else if err != nil {
		result.Error = err.Error()
	}
	if len(stderr.data) > 0 && result.Error != "" {
		text := strings.TrimSpace(strings.ToValidUTF8(string(stderr.data), "�"))
		result.Error += "：" + text
	}
	if len(result.Error) > programErrorLimit {
		result.Error = result.Error[:programErrorLimit]
		for !utf8.ValidString(result.Error) {
			result.Error = result.Error[:len(result.Error)-1]
		}
	}
	result.Success = err == nil && ctx.Err() == nil
	return result
}
