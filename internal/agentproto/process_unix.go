//go:build !windows

package agentproto

import (
	"os/exec"
	"syscall"
)

// detach starts the agent in a new session: it has no controlling terminal,
// so /dev/tty does not reach the pane, and it leads its own process group.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// kill ends the agent's process group, so a command it started goes too.
func kill(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	_ = cmd.Process.Kill()
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
