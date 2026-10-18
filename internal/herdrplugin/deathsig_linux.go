package herdrplugin

import (
	"os/exec"
	"syscall"
)

// deathSignal kills the command when tuios ends, so a killed install does
// not leave its build running. detach set SysProcAttr already.
func deathSignal(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}
