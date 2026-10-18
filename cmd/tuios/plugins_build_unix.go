//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// buildGroup puts a build command in a process group of its own, so a
// timeout or Ctrl-C kills every process the build started.
func buildGroup(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
}
