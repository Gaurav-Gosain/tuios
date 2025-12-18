//go:build windows

package session

import (
	"os/exec"
)

// configurePTYCommand sets up the command for PTY usage on Windows.
// Windows ConPTY handles the terminal setup differently than Unix PTYs.
func configurePTYCommand(cmd *exec.Cmd) {
	// Windows ConPTY doesn't require special SysProcAttr setup
	// The creack/pty library handles the ConPTY creation internally
}
