//go:build darwin

package session

import (
	"net"

	"golang.org/x/sys/unix"
)

const peerPIDSupported = true

func readProcLineage(pid int) (ppid int, tty int64, ok bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil || int(kp.Proc.P_pid) != pid {
		return 0, 0, false
	}
	dev := int64(kp.Eproc.Tdev)
	if dev == -1 {
		dev = 0
	}
	return int(kp.Eproc.Ppid), dev, true
}

func readProcLink(pid int) (ppid int, tty int64, start uint64, ok bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil || int(kp.Proc.P_pid) != pid {
		return 0, 0, 0, false
	}
	dev := int64(kp.Eproc.Tdev)
	if dev == -1 {
		dev = 0
	}
	tv := kp.Proc.P_starttime
	return int(kp.Eproc.Ppid), dev, uint64(tv.Sec)*1_000_000 + uint64(tv.Usec), true //nolint:gosec // a start time is not negative
}

func peerUID(conn net.Conn) (uid int, ok bool) {
	uc, isUnix := conn.(*net.UnixConn)
	if !isUnix {
		return 0, false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, false
	}
	_ = raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err == nil && cred != nil {
			uid, ok = int(cred.Uid), true
		}
	})
	return uid, ok
}
