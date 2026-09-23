//go:build darwin

package terminal

import (
	"bytes"

	"golang.org/x/sys/unix"
)

// procComm returns the command name of a process, or "" when it cannot be read.
//
// macOS has no procfs. The kinfo_proc from the kern.proc.pid sysctl carries the
// same short name Linux keeps in /proc/<pid>/comm, truncated by the kernel, and
// an ordinary user can read it for the processes a pane spawned.
func procComm(pid int) string {
	if pid <= 0 {
		return ""
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil {
		return ""
	}
	name := kp.Proc.P_comm[:]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	return string(name)
}
