//go:build !windows

package provider

import "os/exec"

type quotaProcessJob struct{}

func newQuotaProcessJob() (*quotaProcessJob, error) { return &quotaProcessJob{}, nil }
func (*quotaProcessJob) attach(*exec.Cmd) error     { return nil }
func (*quotaProcessJob) close()                     {}
