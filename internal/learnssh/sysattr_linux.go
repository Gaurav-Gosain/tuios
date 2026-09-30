package learnssh

import (
	"os/exec"
	"syscall"
)

// setChildAttrs puts the session in its own process group, and has the
// kernel kill it if the server dies first.
func setChildAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}

// killChild stops the session's process group.
func killChild(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
