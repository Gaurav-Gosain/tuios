//go:build !unix

package app

import (
	"os"
	"os/exec"
)

// Windows has no process group to put a shell in, so the timeout kills the
// child it started and the wait grace closes the pipes an escaped grandchild
// would otherwise hold open.

func dockGroupCommand(*exec.Cmd) {}

func dockKillGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Kill()
}
