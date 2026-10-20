//go:build unix

package transcriptview

import (
	"os/exec"
	"syscall"
	"time"
)

// runTree runs the export in a process group of its own and kills the whole
// group when its context ends: opencode starts a private server under it,
// which would outlive a kill of the command alone.
func runTree(cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 500 * time.Millisecond
	return cmd.Run()
}
