//go:build unix && !js

package session

import (
	"os"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

// Every process of the panes ends with the daemon.
//
// Closing a pane hangs up the process groups of its session (killPaneTree),
// as kill-pane does in tmux: a job that ignores SIGHUP runs on. When the
// daemon stops, that left such a job running with no pane, no terminal and
// no daemon, for example an agent's loop at full speed, reparented to init
// or to the user manager. So a stopping daemon reads the processes of each
// pane's session before the panes close, and after they close, each one
// still running gets SIGHUP and SIGCONT, and after paneReapGrace, SIGKILL.
//
// A process that left the pane's session with setsid is not one of them, as
// for kill-pane: a person who ran setsid asked for it, and tuios uses it on
// purpose, for the worker of restart-server and for a daemon started from a
// pane. The scope of a pane under a systemd user service (panescope) is
// left to end with its last process, for the same reason: the worker of a
// restart started from a pane runs in that pane's scope.
//
// A live upgrade hands the panes over without stopping the daemon, so this
// never runs for one. A crash runs nothing: the panes then get the hangup a
// closed terminal sends, and no more.

// paneReapGrace is how long a pane's process has to end on the hangup
// before it is killed.
const paneReapGrace = time.Second

// reapProc is one process of a pane, with its start time, so a pid that a
// new program takes later is never signalled.
type reapProc struct {
	pid   int
	start uint64
}

// paneProcs lists every process in the session of each pane's shell. A pane
// shell leads its session (ptyspawn starts it with setsid). The daemon is
// left out.
func (d *Daemon) paneProcs() []reapProc {
	self := os.Getpid()
	seen := map[int]bool{}
	var out []reapProc
	for _, s := range d.manager.AllSessions() {
		for _, p := range s.allPTYs() {
			if p.proc == nil {
				continue
			}
			leader := p.proc.Pid
			if sid, ok := procinfo.SessionID(leader); !ok || sid != leader {
				continue
			}
			for _, pid := range append([]int{leader}, procinfo.SessionMembers(leader)...) {
				if pid <= 1 || pid == self || seen[pid] {
					continue
				}
				seen[pid] = true
				if st, ok := procinfo.StartTime(pid); ok {
					out = append(out, reapProc{pid: pid, start: st})
				}
			}
		}
	}
	return out
}

// alive reports whether p still runs: the same process, not a zombie.
func (p reapProc) alive() bool {
	st, ok := procinfo.StartTime(p.pid)
	if !ok || st != p.start {
		return false
	}
	return !zombie(p.pid)
}

// reapPaneProcs ends each of procs that still runs: SIGHUP and SIGCONT, then
// SIGKILL after grace. It returns how many it had to kill.
func reapPaneProcs(procs []reapProc, grace time.Duration) int {
	var left []reapProc
	for _, p := range procs {
		if p.alive() {
			_ = syscall.Kill(p.pid, syscall.SIGHUP)
			_ = syscall.Kill(p.pid, syscall.SIGCONT)
			left = append(left, p)
		}
	}
	deadline := time.Now().Add(grace)
	for len(left) > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		still := left[:0]
		for _, p := range left {
			if p.alive() {
				still = append(still, p)
			}
		}
		left = still
	}
	killed := 0
	for _, p := range left {
		if p.alive() && syscall.Kill(p.pid, syscall.SIGKILL) == nil {
			killed++
		}
	}
	return killed
}
