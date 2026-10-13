//go:build darwin

package session

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerPIDSupported says the daemon on this platform reads the peer's pid, so
// it places a caller in its pane without the pane's token.
const peerPIDSupported = true

// The darwin half of the peer checks in human_origin.go: LOCAL_PEERPID for who
// is on the other end of a socket, and the kern.proc sysctls for where that
// process came from. LOCAL_PEERCRED would give the uid and not the pid, and the
// socket's mode already limits it to one uid; the pid is what the checks need.

// readProcLineage returns a process's parent pid and its controlling terminal
// from its kinfo_proc. tty is 0 for a process with no controlling terminal. ok
// is false when the process cannot be read.
func readProcLineage(pid int) (ppid int, tty int64, ok bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil || int(kp.Proc.P_pid) != pid {
		return 0, 0, false
	}
	dev := int64(kp.Eproc.Tdev)
	if dev == -1 { // NODEV
		dev = 0
	}
	return int(kp.Eproc.Ppid), dev, true
}

// peerUID returns the uid of the process on the other end of a unix socket,
// from LOCAL_PEERCRED. ok is false when conn is not a unix socket or the
// kernel does not say.
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
