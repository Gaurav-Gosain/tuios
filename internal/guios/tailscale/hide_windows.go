package tailscale

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// hide starts tailscale without a console: the app and kerod have none,
// and each run would open one.
func hide(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
