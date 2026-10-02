//go:build darwin

package session

import "golang.org/x/sys/unix"

// readForegroundPGID returns the foreground process group of the process's
// controlling terminal, the e_tpgid of its kinfo_proc.
func readForegroundPGID(pid int) (int, bool) {
	if pid <= 0 {
		return 0, false
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil {
		return 0, false
	}
	tpgid := int(kp.Eproc.Tpgid)
	if tpgid <= 0 {
		return 0, false
	}
	return tpgid, true
}
