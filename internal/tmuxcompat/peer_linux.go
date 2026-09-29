//go:build linux

package tmuxcompat

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerPID returns the pid of the process on the other end of a unix socket,
// as the kernel recorded it at connect time (SO_PEERCRED), or 0 when it does
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
		if cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED); err == nil && cred != nil {
			pid = int(cred.Pid)
		}
	})
	return pid
}
