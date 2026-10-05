//go:build linux

package session

import (
	"os"
	"strconv"
	"strings"
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

// readArgvExact returns a process's arguments with empty ones kept. readCmdline
// drops them, which is right for naming a process and wrong for replaying one:
// an empty value (ssh -o "") would pair the next argument with the option.
func readArgvExact(pid int) []string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(data) == 0 {
		return nil
	}
	s := string(data)
	if s[len(s)-1] == 0 {
		s = s[:len(s)-1]
	}
	return strings.Split(s, "\x00")
}

// readEnvVarOf reads one variable of a process's environment. The file is
// readable only for a process of the same user.
func readEnvVarOf(pid int, name string) (string, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return "", false
	}
	return environVar(data, name)
}

// fileOwner is the uid that owns a file.
func fileOwner(fi os.FileInfo) (int, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}
