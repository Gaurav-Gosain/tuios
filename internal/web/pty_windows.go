//go:build windows

package web

import (
	"os/exec"
)

// setupPTYCommand configures the command for Windows ConPTY.
// Windows ConPTY handles session management differently.
func setupPTYCommand(cmd *exec.Cmd) {
	// No special setup needed for Windows ConPTY
	// xpty handles Windows-specific setup internally
}
