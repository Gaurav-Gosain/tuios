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

// processName reads p_comm from the process's kinfo_proc, empty when it
// cannot be read.
func processName(pid int) string {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil {
		return ""
	}
	b := kp.Proc.P_comm[:]
	for i, c := range b {
		if c == 0 {
			b = b[:i]
			break
		}
	}
	return string(b)
}
