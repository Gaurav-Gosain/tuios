//go:build linux

package session

import (
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

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
	ppid, ok = procinfo.ParentPID(pid)
	if !ok {
		return 0, 0, false
	}
	return ppid, int(st.Uid), true
}

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
