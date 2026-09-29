//go:build darwin

package procinfo

import (
	"net"

	"golang.org/x/sys/unix"
)

// PeerPID returns the pid of the process on the other end of a unix socket,
// as the kernel recorded it at connect time (LOCAL_PEERPID), or 0 when it does
// not say.
func PeerPID(conn net.Conn) int {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0
	}
	pid := 0
	_ = raw.Control(func(fd uintptr) {
		if v, err := unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID); err == nil {
			pid = v
		}
	})
	return pid
}

// StartTime returns when process pid started, in microseconds since the
// epoch, and false when it cannot be read.
func StartTime(pid int) (uint64, bool) {
	if pid <= 0 {
		return 0, false
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil || int(kp.Proc.P_pid) != pid {
		return 0, false
	}
	tv := kp.Proc.P_starttime
	return uint64(tv.Sec)*1_000_000 + uint64(tv.Usec), true
}
