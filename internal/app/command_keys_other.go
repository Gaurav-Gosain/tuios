//go:build windows || js

package app

import "os/exec"

// detachFromTerminal does nothing here: there is no controlling terminal to
// leave. See runCommandShell.
func detachFromTerminal(*exec.Cmd) {}
