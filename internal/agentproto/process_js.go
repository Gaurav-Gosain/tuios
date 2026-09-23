//go:build js

package agentproto

import "os/exec"

// detach is a no-op: the browser build has no processes, so there is no
// session or console to leave. It is here so the package compiles for the
// Learn tour, which links the daemon code but never starts an agent.
func detach(_ *exec.Cmd) {}

// kill ends the agent, for symmetry with the other platforms.
func kill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
