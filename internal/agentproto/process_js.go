//go:build js

package agentproto

import "os/exec"

func detach(_ *exec.Cmd) {}

func kill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
