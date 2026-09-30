//go:build unix

package app

import (
	"os/exec"
	"syscall"
)

// prepareClipCmd puts a clipboard helper in a process group of its own, so a
// timeout can end it and everything it started.
func prepareClipCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killClipCmd kills a clipboard helper's whole process group.
func killClipCmd(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
