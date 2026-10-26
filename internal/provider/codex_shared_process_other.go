//go:build !windows

package provider

import "os/exec"

func startSharedCodexCommand(cmd *exec.Cmd) (func(), error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return func() { _ = cmd.Process.Kill() }, nil
}
