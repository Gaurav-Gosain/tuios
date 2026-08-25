//go:build windows

package ptyspawn

import (
	"os/exec"
)

// configureCommand is a no-op on Windows: ConPTY sets the child up itself, and
// there is no controlling-terminal ioctl to be refused.
func configureCommand(_ *exec.Cmd) {}
