//go:build windows

package provider

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func configureRPCProcess(cmd *exec.Cmd) {
	// These helpers communicate through pipes. SW_HIDE alone still allocates a
	// console when launched by the GUI and can flash its window during startup.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
