//go:build linux

package integration

import (
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func selfSID() int {
	sid, err := unix.Getsid(0)
	if err != nil {
		return 0
	}
	return sid
}

// parentPID reads field 4 of /proc/<pid>/stat, and the current process's
// parent for pid 0. The command name in field 2 may hold spaces and
// parentheses, so fields are counted after its closing parenthesis.
func parentPID(pid int) int {
	if pid == 0 {
		return os.Getppid()
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	s := string(data)
	end := strings.LastIndexByte(s, ')')
	if end < 0 {
		return 0
	}
	f := strings.Fields(s[end+1:])
	if len(f) < 2 {
		return 0
	}
	ppid, err := strconv.Atoi(f[1])
	if err != nil {
		return 0
	}
	return ppid
}
