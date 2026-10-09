//go:build linux || darwin

package module

import (
	"os/exec"
	"syscall"
)

func devProcessGroup(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

func devSignalGroup(pid int, kill bool) error {
	signal := syscall.SIGTERM
	if kill {
		signal = syscall.SIGKILL
	}
	return syscall.Kill(-pid, signal)
}
