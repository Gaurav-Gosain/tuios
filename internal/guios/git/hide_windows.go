package git

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// hide starts git without a console: kerod has none, and each run would
// open one.
func hide(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
