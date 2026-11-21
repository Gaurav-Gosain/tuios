//go:build linux

package herdrplugin

import (
	"golang.org/x/sys/unix"
)

const waitsForGroup = true

func becomeSubreaper() { _ = unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) }

func waitForGroup() {}

func waitExited(pid int) {
	var info unix.Siginfo
	for unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil) == unix.EINTR {
	}
}
