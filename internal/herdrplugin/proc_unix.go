//go:build unix

package herdrplugin

import (
	"os/exec"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

type procGroup struct{ cmd *exec.Cmd }

func track(cmd *exec.Cmd) (*procGroup, error) { return &procGroup{cmd: cmd}, nil }

func (g *procGroup) kill() {
	if g.cmd.Process == nil {
		return
	}
	sid := g.cmd.Process.Pid
	_ = syscall.Kill(-sid, syscall.SIGKILL)
	for _, pid := range procinfo.SessionMembers(sid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func (g *procGroup) release() {}
