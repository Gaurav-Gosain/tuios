//go:build !unix

package childproc

import "os/exec"

// ownGroup is a no-op where there are no process groups to end. WaitDelay
// still bounds the wait.
func ownGroup(*exec.Cmd) {}
