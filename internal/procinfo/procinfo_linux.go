//go:build linux

package procinfo

import (
	"net"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// PeerPID returns the pid of the process on the other end of a unix socket,
// as the kernel recorded it at connect time (SO_PEERCRED), or 0 when it does
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
		if cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED); err == nil && cred != nil {
			pid = int(cred.Pid)
		}
	})
	return pid
}

// StartTime returns when process pid started, in clock ticks since boot
// (field 22 of /proc/<pid>/stat), and false when it cannot be read.
func StartTime(pid int) (uint64, bool) {
	if pid <= 0 {
		return 0, false
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	s := string(data)
	rparen := strings.LastIndex(s, ")")
	if rparen < 0 {
		return 0, false
	}
	// The fields after "(comm) " start at field 3, so field 22 is index 19.
	fields := strings.Fields(s[rparen+1:])
	if len(fields) < 20 {
		return 0, false
	}
	v, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
