//go:build !windows

package provider

import "os/exec"

func configureRPCProcess(*exec.Cmd) {}
