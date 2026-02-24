//go:build windows

package protodec

import (
	"os/exec"
	"syscall"
)

func hideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		return
	}
	cmd.SysProcAttr.HideWindow = true
}
