//go:build !unix

package dockengine

import (
	"os"
	"os/exec"
)

func dockGroupCommand(*exec.Cmd) {}

func dockKillGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Kill()
}
