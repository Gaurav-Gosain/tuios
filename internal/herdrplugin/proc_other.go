//go:build !unix && !windows

package herdrplugin

import "os/exec"

// detach does nothing where there is no session to start: the process has
// no console of tuios's to draw on, since its output goes to the log.
func detach(*exec.Cmd) {}

// procGroup is the command alone: there is no group to kill with one call.
type procGroup struct{ cmd *exec.Cmd }

func track(cmd *exec.Cmd) (*procGroup, error) { return &procGroup{cmd: cmd}, nil }

func (g *procGroup) kill() {
	if g.cmd.Process != nil {
		_ = g.cmd.Process.Kill()
	}
}

func (g *procGroup) release() {}
