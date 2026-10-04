//go:build unix

package herdrplugin

import (
	"os/exec"
	"syscall"
)

// detach gives the command a session of its own: no controlling terminal,
// so nothing it runs can draw on the tuios screen, and a process group the
// runner can kill whole.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// killGroup kills the process group the command leads. The group id is the
// process id, which Setsid made so.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
