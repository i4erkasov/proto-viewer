//go:build !windows

package protodec

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
