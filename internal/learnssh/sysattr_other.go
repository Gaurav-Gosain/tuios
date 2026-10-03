//go:build !linux

package learnssh

import "os/exec"

func setChildAttrs(*exec.Cmd) {}

func killChild(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
