//go:build windows

package provider

import (
	"golang.org/x/sys/windows"
	"os/exec"
	"unsafe"
)

// A quota query owns its Node helper and all descendants. Closing the job is
// the last-resort cleanup even when Node/SDK cannot handle cancellation.
type quotaProcessJob struct{ handle windows.Handle }

func newQuotaProcessJob() (*quotaProcessJob, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	return &quotaProcessJob{h}, nil
}
func (j *quotaProcessJob) attach(cmd *exec.Cmd) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.AssignProcessToJobObject(j.handle, h)
}
func (j *quotaProcessJob) close() { windows.CloseHandle(j.handle) }
