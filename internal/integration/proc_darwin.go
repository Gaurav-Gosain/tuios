//go:build darwin

package integration

import (
	"os"

	"golang.org/x/sys/unix"
)

func selfSID() int {
	sid, err := unix.Getsid(0)
	if err != nil {
		return 0
	}
	return sid
}

// parentPID reads e_ppid from the process's kinfo_proc, and the current
// process's parent for pid 0.
func parentPID(pid int) int {
	if pid == 0 {
		return os.Getppid()
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil {
		return 0
	}
	return int(kp.Eproc.Ppid)
}
