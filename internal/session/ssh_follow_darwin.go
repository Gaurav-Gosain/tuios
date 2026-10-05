//go:build darwin

package session

import "golang.org/x/sys/unix"

// readParentAndOwner returns a process's parent pid and its effective user,
// from the kinfo_proc the kernel gives for it.
func readParentAndOwner(pid int) (ppid, uid int, ok bool) {
	if pid <= 0 {
		return 0, 0, false
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil || int(kp.Proc.P_pid) != pid {
		return 0, 0, false
	}
	return int(kp.Eproc.Ppid), int(kp.Eproc.Ucred.Uid), true
}
