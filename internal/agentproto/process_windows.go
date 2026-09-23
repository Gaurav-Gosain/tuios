//go:build windows

package agentproto

import (
	"os/exec"
	"syscall"
)

// detachedProcess is DETACHED_PROCESS: the agent gets no console, so it cannot
// write to the pane's past the transcript.
const detachedProcess = 0x00000008

// detach starts the agent without a console.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | syscall.CREATE_NEW_PROCESS_GROUP}
}

// kill ends the agent.
func kill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
