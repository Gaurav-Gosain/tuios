//go:build linux

package session

import (
	"net"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
	"golang.org/x/sys/unix"
)

// peerPIDSupported says the daemon on this platform reads the peer's pid, so
// it places a caller in its pane without the pane's token.
const peerPIDSupported = true

// The Linux half of the peer checks in human_origin.go: SO_PEERCRED for who is
// on the other end of a socket, and procfs for where that process came from.

// peerUID returns the uid of the process on the other end of a unix socket,
// as the kernel recorded it at connect time. ok is false when conn is not a
// unix socket or the kernel does not say.
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
		cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if err == nil && cred != nil {
			uid, ok = int(cred.Uid), true
		}
	})
	return uid, ok
}

// readProcLineage returns a process's parent pid and its controlling terminal,
// fields 4 and 7 of /proc/<pid>/stat. tty is 0 for a process with no
// controlling terminal. ok is false when the process cannot be read.
func readProcLineage(pid int) (ppid int, tty int64, ok bool) {
	stat, ok := procinfo.Stat(pid)
	if !ok {
		return 0, 0, false
	}
	parent, ok := procinfo.StatField(stat, 4)
	if !ok {
		return 0, 0, false
	}
	nr, _ := procinfo.StatField(stat, 7)
	return parent, int64(nr), true
}
