//go:build windows

package terminal

import (
	"os/exec"
)

// setupPTYCommand configures the command for Windows ConPTY
// Windows handles PTY setup differently, so this is a no-op
func setupPTYCommand(cmd *exec.Cmd) {
	// Windows ConPTY handles everything automatically
	// No special SysProcAttr configuration needed
}
