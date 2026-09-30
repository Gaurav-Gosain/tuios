//go:build !windows && !js

package app

import (
	"os/exec"
	"syscall"
)

// detachFromTerminal starts cmd in a session of its own, with no controlling
// terminal. See runCommandShell.
func detachFromTerminal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
