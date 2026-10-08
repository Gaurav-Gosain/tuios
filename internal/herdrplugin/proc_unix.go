//go:build unix

package herdrplugin

import (
	"os/exec"
	"syscall"
)

// detach gives the command a session of its own: no controlling terminal,
// so nothing it runs can draw on the tuios screen, and a process group the
// runner can kill whole.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// procGroup is the process group the command leads.
type procGroup struct{ cmd *exec.Cmd }

// track records the started command's group. It cannot fail here.
func track(cmd *exec.Cmd) (*procGroup, error) { return &procGroup{cmd: cmd}, nil }

// kill kills the process group the command leads. The group id is the
// process id, which Setsid made so.
func (g *procGroup) kill() {
	if g.cmd.Process != nil {
		_ = syscall.Kill(-g.cmd.Process.Pid, syscall.SIGKILL)
	}
}

// release reports that nothing is kept once the command is reaped: after
// Wait the group id may name another group, so it is not killed later.
func (g *procGroup) release() bool { return true }

// drop does nothing: nothing is kept. See release.
func (g *procGroup) drop() {}
