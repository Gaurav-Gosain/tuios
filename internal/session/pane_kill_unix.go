//go:build unix && !js

package session

import (
	"os"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

// killPaneTree hangs up a closing pane, as a terminal that goes away does,
// and then kills its shell.
//
// A pane's shell starts with setsid (ptyspawn), so its pid is the id of the
// session that holds the shell, its jobs and what they started, also the ones
// in process groups of their own. Each process group of that session gets
// SIGHUP, and SIGCONT so a stopped job can act on it. A job that takes the
// default action ends: a plain background job, a dev server, a sleep. A job
// that ignores SIGHUP (nohup) runs on, and so does one that left the session
// with setsid. That is what tmux does on kill-pane, and what a person who
// used nohup or setsid asked for.
//
// The kernel's own hangup, when the master closes, reaches only the
// terminal's foreground process group, and a shell killed with SIGKILL hangs
// up none of its jobs, so before this every background job outlived its pane.
//
// The groups are read before the shell is killed. The kernel cannot reuse a
// pid that is still the id of a live process group or session, so every group
// signalled is one of the pane's. The shell's pid is checked to lead its
// session first, so a pid already reused for another program selects nothing.
func killPaneTree(proc *os.Process) {
	if proc == nil {
		return
	}
	for _, pgid := range paneGroups(proc.Pid) {
		_ = syscall.Kill(-pgid, syscall.SIGHUP)
		_ = syscall.Kill(-pgid, syscall.SIGCONT)
	}
	_ = proc.Kill()
}

// paneGroups lists the process groups of the session that leader leads, and
// nothing when leader does not lead a session. The daemon's own group is left
// out, so a daemon that somehow shares the session cannot hang itself up.
func paneGroups(leader int) []int {
	if leader <= 1 {
		return nil
	}
	if sid, ok := procinfo.SessionID(leader); !ok || sid != leader {
		return nil
	}
	own := syscall.Getpgrp()
	seen := map[int]bool{}
	var out []int
	for _, pid := range append([]int{leader}, procinfo.SessionMembers(leader)...) {
		pgid, err := syscall.Getpgid(pid)
		if err != nil || pgid <= 1 || pgid == own || seen[pgid] {
			continue
		}
		seen[pgid] = true
		out = append(out, pgid)
	}
	return out
}
