//go:build unix

package web

import (
	"os/exec"
	"syscall"
)

// setupPTYCommand configures the command to use the PTY as controlling terminal.
// This is required for shells to work properly on Unix systems.
func setupPTYCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true, // Create new session
		Setctty: true, // Set controlling terminal
		Ctty:    0,    // Use stdin (which will be the PTY slave)
	}
}
