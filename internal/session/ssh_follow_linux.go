//go:build linux

package session

import (
	"os"
	"strconv"
	"syscall"
)

// readParentAndOwner returns a process's parent pid and the user that owns it,
// from procfs. The owner of /proc/<pid> is the process's effective uid.
func readParentAndOwner(pid int) (ppid, uid int, ok bool) {
	dir := "/proc/" + strconv.Itoa(pid)
	fi, err := os.Stat(dir)
	if err != nil {
		return 0, 0, false
	}
	st, isStat := fi.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, 0, false
	}
	data, err := os.ReadFile(dir + "/stat")
	if err != nil {
		return 0, 0, false
	}
	ppid, ok = parseStatField(string(data), 4)
	if !ok {
		return 0, 0, false
	}
	return ppid, int(st.Uid), true
}
