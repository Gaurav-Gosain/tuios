//go:build !windows

package childproc

import "os/exec"

// Hide does nothing here: a child of the daemon opens no window off Windows.
func Hide(*exec.Cmd) {}
