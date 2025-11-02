//go:build unix

package terminal

import (
	"os/exec"
	"syscall"
)

// setupPTYCommand configures the command to use the PTY as controlling terminal
// This is required for shells like fish to work properly on Unix systems
func setupPTYCommand(cmd *exec.Cmd) {
	// Set up the command to use the PTY as controlling terminal
	// Note: Ctty is the FD number in the child process (0 = stdin)
	// xpty.Start() will set stdin to the PTY slave
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true, // Create new session
		Setctty: true, // Set controlling terminal
		Ctty:    0,    // Use stdin (which will be the PTY slave)
	}
}
