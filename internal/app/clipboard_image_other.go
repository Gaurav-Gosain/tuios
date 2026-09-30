//go:build !unix

package app

import "os/exec"

// prepareClipCmd has nothing to set where there are no process groups. The
// pipe is closed on a timeout instead, which ends the read whatever a child
// of the helper still holds.
func prepareClipCmd(*exec.Cmd) {}

// killClipCmd kills a clipboard helper.
func killClipCmd(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
