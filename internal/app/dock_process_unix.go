//go:build unix

package app

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// A dock component is a shell command, and a shell command is a tree. The two
// helpers here are what let the timeout act on the tree instead of on its root.
//
// The bug they exist for: `sh -c` hands the command to a shell, and the shell
// forks for anything that is not a single simple command. The fork inherits the
// stdout pipe. Killing the shell alone leaves that child holding the write end,
// so the read side never sees EOF and the wait never returns, however long ago
// the deadline passed. dash made this visible on Debian and Ubuntu, but the
// trigger is the fork, not the shell: bash forks too for `a; b`, for `a & wait`,
// for a subshell and for a pipeline.

// dockGroupCommand puts the shell and everything it starts into one new process
// group, so there is a group for dockKillGroup to signal.
func dockGroupCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// dockKillGroup kills the whole group dockGroupCommand made.
//
// The negative pid is the point: it reaches the shell's children as well as the
// shell, which is what closes the inherited pipe and what stops a component that
// times out every interval from leaking a process every interval.
func dockKillGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, syscall.ESRCH):
		// The group is already gone. Reporting this as done keeps os/exec from
		// inventing an error for a command that simply finished.
		return os.ErrProcessDone
	default:
		// Setpgid was refused, or the group is not ours. Kill what is reachable
		// rather than nothing.
		return cmd.Process.Kill()
	}
}
