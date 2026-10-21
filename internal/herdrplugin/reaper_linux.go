//go:build linux

package herdrplugin

import (
	"golang.org/x/sys/unix"
)

// waitsForGroup: as a subreaper, the reaper's Wait4 loop sees every process
// the command left, and ends when none is left.
const waitsForGroup = true

func becomeSubreaper() { _ = unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) }

// waitForGroup has nothing left to do: the Wait4 loop ran until no child
// was left.
func waitForGroup() {}

// waitExited blocks until process pid exits, and leaves it unreaped, so its
// pid stays taken while the caller drops what it knew about it.
func waitExited(pid int) {
	var info unix.Siginfo
	for unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil) == unix.EINTR {
	}
}
