//go:build windows

package provider

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"

	"golang.org/x/sys/windows"
)

var resumeSharedCodexProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

// Assign the suspended app-server to an owned job before it can create its
// code-mode host or workers. No user/global daemon process is touched.
func startSharedCodexCommand(cmd *exec.Cmd) (func(), error) {
	job, err := newQuotaProcessJob()
	if err != nil {
		return nil, err
	}
	var once sync.Once
	stop := func() { once.Do(job.close) }
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_SUSPENDED}
	cmd.Cancel = func() error { stop(); return cmd.Process.Kill() }
	if err = cmd.Start(); err != nil {
		stop()
		return nil, err
	}
	fail := func(err error) (func(), error) { cmd.Process.Kill(); cmd.Wait(); stop(); return nil, err }
	if err = job.attach(cmd); err != nil {
		return fail(err)
	}
	process, err := windows.OpenProcess(windows.PROCESS_SUSPEND_RESUME, false, uint32(cmd.Process.Pid))
	if err != nil {
		return fail(err)
	}
	result, _, _ := resumeSharedCodexProcess.Call(uintptr(process))
	windows.CloseHandle(process)
	if result != 0 {
		return fail(fmt.Errorf("启动共享 Codex 失败: NTSTATUS 0x%x", result))
	}
	return stop, nil
}
