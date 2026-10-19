//go:build unix

package transcriptview

import (
	"os/exec"
	"syscall"
	"time"
)

// killTreeOnCancel puts the export in a process group of its own and kills
// the whole group when its context ends: opencode starts a private server
// under it, which would outlive a kill of the command alone.
func killTreeOnCancel(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 500 * time.Millisecond
}
