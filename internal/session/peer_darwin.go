//go:build darwin

package session

import (
	"net"

	"golang.org/x/sys/unix"
)

// The darwin half of the peer checks in human_origin.go: LOCAL_PEERPID for who
// is on the other end of a socket, and the kern.proc sysctls for where that
// process came from. LOCAL_PEERCRED would give the uid and not the pid, and the
// socket's mode already limits it to one uid; the pid is what the checks need.

// peerPID returns the pid of the process on the other end of a unix socket, as
// the kernel recorded it at connect time, or 0 when conn is not a unix socket or
// the kernel does not say.
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

// readProcEnvVar reads one variable from the environment a process was
// started with.
func readProcEnvVar(pid int, name string) (string, bool) {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", false
	}
	return procargsEnvVar(buf, name)
}
