//go:build unix

package app

import (
	"os/exec"
	"syscall"
)

// detachProcess puts cmd in a session of its own, so a signal to tuios does
// not reach a program the launcher started.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
