//go:build !windows

package session

import (
	"os/exec"
	"syscall"
)

// configurePTYCommand sets up the command for PTY usage on Unix systems.
// This creates a new session and sets up the controlling terminal.
func configurePTYCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true, // Create new session
		Setctty: true, // Set controlling terminal
		Ctty:    0,    // Use stdin (which will be the PTY slave)
	}
}
