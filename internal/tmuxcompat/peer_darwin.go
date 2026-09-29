//go:build darwin

package tmuxcompat

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerPID returns the pid of the process on the other end of a unix socket,
// as the kernel recorded it at connect time (LOCAL_PEERPID), or 0 when it does
// not say.
func peerPID(conn net.Conn) int {
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
