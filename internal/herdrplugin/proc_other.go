//go:build !unix

package herdrplugin

import "os/exec"

// detach does nothing where there is no session to start: the process has
// no console of tuios's to draw on, since its output goes to the log.
func detach(*exec.Cmd) {}

// killGroup kills the process. Windows has no process group to kill with
// one call, so a child the command started may outlive it.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
