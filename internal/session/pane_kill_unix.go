//go:build unix && !js

package session

import (
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

// killPaneTree hangs up a closing pane, as a terminal that goes away does,
// and kills its program if it still runs after paneCloseGrace.
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
//
// The pane's own program gets the hangup like every job, and then a grace to
// act on it before SIGKILL. A shell ends on the hangup at once. A program run
// as the pane command (new-window -- PROG, or a wrapper that execs it) may
// save its state on SIGHUP, and a SIGKILL sent with the hangup ended it
// before it could. The wait runs in a goroutine and ends when exited closes,
// so the close returns at once. exited is the pane's exit, which closes after
// the reap; proc.Kill on a reaped process does nothing, and on Linux it signals
// through a pidfd, so a reused pid is never hit.
func killPaneTree(proc *os.Process, exited <-chan struct{}) {
	if proc == nil {
		return
	}
	groups := paneGroups(proc.Pid)
	for _, pgid := range groups {
		_ = syscall.Kill(-pgid, syscall.SIGHUP)
		_ = syscall.Kill(-pgid, syscall.SIGCONT)
	}
	if len(groups) == 0 || exited == nil {
		// Not a session leader: it got no hangup, so there is nothing to wait
		// for.
		_ = proc.Kill()
		return
	}
	done := startPaneGrace()
	go func() {
		defer endPaneGrace(done)
		grace := time.NewTimer(paneCloseGrace)
		defer grace.Stop()
		select {
		case <-exited:
		case <-grace.C:
			_ = proc.Kill()
			// The grace ends with the reap, so a live upgrade that waits
			// for it leaves no zombie that the new image does not know. A
			// closed pane is out of its session, so no handoff shuts its
			// reap gate. The bound is for a process stuck in the kernel.
			grace.Reset(paneCloseGrace)
			select {
			case <-exited:
			case <-grace.C:
			}
		}
	}()
}

// paneCloseGrace is how long a closing pane's program has to act on the
// hangup. Saving a session or a buffer to a local disk takes milliseconds, so
// one second leaves a wide margin. It is short because it is the time a
// program that ignores SIGHUP holds kill-server and a live upgrade: both wait
// for the graces to end (waitPaneGraces).
const paneCloseGrace = time.Second

// paneGraces holds the graces still running, each by the channel that closes
// when it ends. Each pane closes on its own, so the graces of a daemon that
// stops all run at once.
var paneGraces = struct {
	sync.Mutex
	running map[chan struct{}]struct{}
}{running: map[chan struct{}]struct{}{}}

func startPaneGrace() chan struct{} {
	done := make(chan struct{})
	paneGraces.Lock()
	paneGraces.running[done] = struct{}{}
	paneGraces.Unlock()
	return done
}

func endPaneGrace(done chan struct{}) {
	paneGraces.Lock()
	delete(paneGraces.running, done)
	paneGraces.Unlock()
	close(done)
}

// waitPaneGraces blocks until every grace that has started by now has ended:
// the pane's program exited or was killed. A grace that starts during the
// wait is not waited for, and no pane that is still open is touched: this
// only waits. A stopping daemon waits here, because a grace that is still
// running when the daemon exits never sends its SIGKILL. A live upgrade waits
// too: the new image does not know a pane that closed, so it would not kill
// its program. Each grace ends after paneCloseGrace, plus the reap of a
// killed program, so this takes about one grace.
func waitPaneGraces() {
	paneGraces.Lock()
	started := make([]chan struct{}, 0, len(paneGraces.running))
	for done := range paneGraces.running {
		started = append(started, done)
	}
	paneGraces.Unlock()
	for _, done := range started {
		<-done
	}
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
