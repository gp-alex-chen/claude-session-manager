//go:build !windows

package notify

import "os/exec"

func configureProgramProcess(cmd *exec.Cmd) {}
func runProgramCommand(cmd *exec.Cmd) error { return cmd.Run() }
