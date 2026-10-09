//go:build !linux && !darwin

package module

import (
	"fmt"
	"os/exec"
)

func devProcessGroup(*exec.Cmd) error {
	return fmt.Errorf("module dev requires Linux, macOS or WSL for process-group shutdown")
}

func devSignalGroup(int, bool) error {
	return nil
}
