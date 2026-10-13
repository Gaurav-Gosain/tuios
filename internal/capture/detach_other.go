//go:build !unix

package capture

import "os/exec"

// detach has nothing to do off unix: the helpers there do not fork a
// selection owner.
func detach(*exec.Cmd) {}
