//go:build !windows

package ptyspawn

import (
	"os/exec"
	"syscall"
)

// configureCommand gives the child its own session and makes the PTY slave its
// controlling terminal. Ctty is the fd number in the child, and xpty.Start puts
// the slave on fd 0, so 0 is the one to claim.
//
// This is the ioctl the kernel occasionally refuses; see the package comment.
func configureCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true, // Create new session
		Setctty: true, // Set controlling terminal
		Ctty:    0,    // Use stdin (which will be the PTY slave)
	}
}
