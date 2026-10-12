//go:build windows

package childproc

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// Hide makes cmd start without a window. It keeps any attributes cmd already
// has, so a caller that sets its own creation flags first loses none of them.
// A caller that replaces cmd.SysProcAttr afterwards drops what Hide set.
func Hide(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}
