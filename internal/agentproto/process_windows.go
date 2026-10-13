//go:build windows

package agentproto

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// detach starts the agent on a console of its own with no window, in a new
// process group. It cannot write to the pane's console past the transcript,
// Ctrl+C in the pane does not reach it, and a console program it starts
// inherits the hidden console instead of opening a window. It keeps the flags
// childproc set.
func detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW | syscall.CREATE_NEW_PROCESS_GROUP
}

// kill ends the agent.
func kill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
