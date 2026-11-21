//go:build !unix && !windows

package herdrplugin

import "os/exec"

func detach(*exec.Cmd) {}

type procGroup struct{ cmd *exec.Cmd }

func track(cmd *exec.Cmd) (*procGroup, error) { return &procGroup{cmd: cmd}, nil }

func (g *procGroup) kill() {
	if g.cmd.Process != nil {
		_ = g.cmd.Process.Kill()
	}
}

func (g *procGroup) release() {}
