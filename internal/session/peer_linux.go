//go:build linux

package session

import (
	"net"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// The Linux half of the peer checks in human_origin.go: SO_PEERCRED for who is
// on the other end of a socket, and procfs for where that process came from.

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
		cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if err == nil && cred != nil {
			pid = int(cred.Pid)
		}
	})
	return pid
}

// readProcLineage returns a process's parent pid and its controlling terminal,
// fields 4 and 7 of /proc/<pid>/stat. tty is 0 for a process with no
// controlling terminal. ok is false when the process cannot be read.
func readProcLineage(pid int) (ppid int, tty int64, ok bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, 0, false
	}
	parent, ok := parseStatField(string(data), 4)
	if !ok {
		return 0, 0, false
	}
	nr, _ := parseStatField(string(data), 7)
	return parent, int64(nr), true
}

// readProcEnvVar reads one variable from the environment a process was
// started with.
func readProcEnvVar(pid int, name string) (string, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return "", false
	}
	return environVar(data, name)
}
