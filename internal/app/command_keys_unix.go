//go:build !windows && !js

package app

import (
	"os/exec"
	"syscall"
)

// detachFromTerminal starts cmd in a session of its own, with no controlling
// terminal. See runCommandShell.
func detachFromTerminal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// killGroupOnCancel makes a cancelled cmd kill its whole process group, so
// a pipeline in sh -c stops with it. The group is the one Setsid made, whose
// id is the process id.
func killGroupOnCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
