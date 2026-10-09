//go:build windows

package provider

import (
	"os/exec"
	"syscall"
)

func configureRPCProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
