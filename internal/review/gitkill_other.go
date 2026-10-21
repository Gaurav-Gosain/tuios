//go:build !linux

package review

import "os/exec"

// killTree is git's Cancel. Off Linux it kills git, and WaitDelay stops the
// wait for what git started.
func killTree(cmd *exec.Cmd) func() error {
	return func() error { return cmd.Process.Kill() }
}
