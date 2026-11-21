//go:build !unix

package app

import "os/exec"

func prepareClipCmd(*exec.Cmd) {}

func killClipCmd(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
