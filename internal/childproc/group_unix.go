//go:build unix

package childproc

import (
	"os/exec"
	"syscall"
)

// ownGroup starts cmd in a process group of its own, and makes its context
// end that whole group: a child cmd started that holds the output open would
// otherwise keep Run waiting past the context.
func ownGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
