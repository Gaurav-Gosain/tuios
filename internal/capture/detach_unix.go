//go:build unix

package capture

import (
	"os/exec"
	"syscall"
)

// detach puts a helper in its own process group. xclip forks a server that
// must outlive the write; without this it dies with its parent and the
// clipboard is empty by the time anyone pastes. It keeps the attributes
// childproc set.
func detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}
