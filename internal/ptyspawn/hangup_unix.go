//go:build !windows && !js

package ptyspawn

import (
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

// HangUp hangs up the session that leader leads, as a terminal that goes
// away does: every process group of that session gets SIGHUP, and SIGCONT so
// a stopped job can act on it. It reports whether leader leads a session, so
// a group got the hangup.
//
// A pane's shell starts with setsid (configureCommand), so its pid is the id
// of the session that holds the shell, its jobs and what they started, also
// the ones in process groups of their own. The kernel's own hangup, when the
// master closes, reaches only the terminal's foreground process group, and a
// shell killed with SIGKILL hangs up none of its jobs. Without this, a
// background job outlives its pane: a loop that writes to the terminal gets
// EIO on every write and runs on with no terminal at all.
//
// A job that ignores SIGHUP (nohup) runs on, and so does one that left the
// session with setsid. That is what tmux does on kill-pane, and what a person
// who used nohup or setsid asked for.
//
// Call it before the leader is reaped: the kernel cannot reuse a pid that is
// still the id of a live process group or session, so every group signalled
// is one of the pane's. The leader is checked to lead its session first, so a
// pid already reused for another program selects nothing. The caller's own
// group is left out, so a caller that somehow shares the session cannot hang
// itself up.
func HangUp(leader int) bool {
	if leader <= 1 {
		return false
	}
	if sid, ok := procinfo.SessionID(leader); !ok || sid != leader {
		return false
	}
	own := syscall.Getpgrp()
	seen := map[int]bool{}
	for _, pid := range append([]int{leader}, procinfo.SessionMembers(leader)...) {
		pgid, err := syscall.Getpgid(pid)
		if err != nil || pgid <= 1 || pgid == own || seen[pgid] {
			continue
		}
		seen[pgid] = true
		_ = syscall.Kill(-pgid, syscall.SIGHUP)
		_ = syscall.Kill(-pgid, syscall.SIGCONT)
	}
	return len(seen) > 0
}
