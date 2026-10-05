//go:build !unix

package session

import "os/exec"

// killGroupOnCancel leaves cmd as it is: no process group to kill here, and
// the context kills the process itself.
func killGroupOnCancel(*exec.Cmd) {}
