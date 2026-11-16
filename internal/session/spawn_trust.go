package session

import (
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

// Which processes this daemon started.
//
// A caller is placed by the nearest process at or above it, in its parent
// chain, that the daemon recorded as one it started (a spawn root):
//
//   - the daemon's own process, unless it shares that process with other
//     code (DaemonConfig.SharedProcess). A daemon's child that no pane shell
//     started, such as git and what a repo's git hook runs, or ssh and its
//     ProxyCommand, is held as inside a pane only by this root.
//   - each pane shell, with the start time it had, read when it was started
//     or adopted. After a keeper brings the previous binary back as a daemon
//     with a new pid, the pane shells are not its children, and this is the
//     root that still places them.
//   - each hosted pane's process, with its start time.
//   - each hook shell running now, with its start time (hooks.Manager).
//
// A pid is a root only with the start time recorded for it, so a pid reused
// after the process exited is never one. The daemon's own pid needs none: it
// cannot be reused while the daemon runs.
//
// The walk compares each parent's start time with its child's. A parent that
// started after its child is not the process that started it: the child's
// parent exited and its pid went to another process between two reads. The
// chain past it is not the caller's, and the walk says so (broken).
//
// Inside a pane is the restricted side for paneOrigin, so the daemon root is
// kept wherever the process is the daemon's. Only a test harness that runs
// the daemon in its own process sets SharedProcess: the harness starts CLI
// calls as its own children, and they are the person, as they are with a
// daemon in a process of its own. See docs/AGENT_STATE.md, "Who can act as
// the person".

// spawnKind is what a spawn root is.
type spawnKind uint8

const (
	spawnDaemon spawnKind = iota + 1
	spawnPane
	spawnHosted
	spawnHook
)

// spawnRoot is one process the daemon started. id is the window of a pane
// and the id of a hosted pane.
type spawnRoot struct {
	kind spawnKind
	pid  int
	id   string
}

// spawnWalk is what a walk up a caller's parents found. tty is the
// controlling terminal of the process the walk started at, read in the same
// record, and readable says that record could be read.
type spawnWalk struct {
	root     spawnRoot
	found    bool
	broken   bool
	readable bool
	tty      int64
}

// procLinkFunc reads a process's parent, controlling terminal and start time.
type procLinkFunc func(pid int) (ppid int, tty int64, start uint64, ok bool)

// procLink is the reader the walk uses: the process table, or the parent and
// start time a test gives in d.hookProc. A test's reader gives no terminal.
func (d *Daemon) procLink() procLinkFunc {
	if fake := d.hookProc; fake != nil {
		return func(pid int) (int, int64, uint64, bool) {
			ppid, start, ok := fake(pid)
			return ppid, 0, start, ok
		}
	}
	return readProcLink
}

// spawnRootOf walks from pid up its parents to a spawn root. start is pid's
// own start time when the caller pinned it at accept, 0 to read it. See
// spawnRootFrom for nearest.
func (d *Daemon) spawnRootOf(pid int, start uint64, nearest bool) spawnWalk {
	ppid, tty, st, ok := d.procLink()(pid)
	if !ok {
		return spawnWalk{}
	}
	if start != 0 {
		st = start
	}
	w := d.spawnRootFrom(pid, st, ppid, nearest)
	w.readable, w.tty = true, tty
	return w
}

// chainLink is one process of a caller's parent chain.
type chainLink struct {
	pid   int
	start uint64
}

// spawnRootFrom is spawnRootOf with pid's own record read: its start time st
// and its parent ppid. paneOriginWith reads that record first, for the
// terminal test that comes before the walk.
//
// The walk checks the daemon and the hooks at each step, and the pane roots
// once, against the whole chain, at the end. A hook shell is never below a
// pane shell, and a pane shell never below a hook shell, so a hook found on
// the way is the nearest root. The daemon is above every pane, so a pane root
// below it is nearer: nearest asks for it (hostedPaneOfPeer). Without nearest
// the daemon is enough, and the pane list is not read.
func (d *Daemon) spawnRootFrom(pid int, st uint64, ppid int, nearest bool) spawnWalk {
	var w spawnWalk
	read := d.procLink()
	hookStart := d.hookStarts
	if hookStart == nil {
		hookStart = d.hooks.HookStart
	}
	var chain [paneOriginMaxDepth]chainLink
	n := 0
	for cur := pid; n < paneOriginMaxDepth && cur > 0; {
		// The daemon is matched by pid alone, which it holds while it runs,
		// and before the stop at init: in a container the daemon can be pid
		// 1 itself.
		if !d.sharedProcess && cur == d.selfPID {
			w.root, w.found = spawnRoot{kind: spawnDaemon, pid: cur}, true
			break
		}
		if n > 0 {
			if cur == 1 {
				// Init is no pane shell or hook shell: those are the
				// daemon's children.
				break
			}
			next, _, start, ok := read(cur)
			if !ok {
				break
			}
			if start > st {
				w.broken = true
				break
			}
			ppid, st = next, start
		}
		chain[n] = chainLink{pid: cur, start: st}
		n++
		if hs, ok := hookStart(cur); ok && hs == st {
			w.root, w.found = spawnRoot{kind: spawnHook, pid: cur}, true
			return w
		}
		cur = ppid
	}
	if w.found && !nearest {
		return w
	}
	if r, ok := d.nearestPaneRoot(chain[:n]); ok {
		w.root, w.found, w.broken = r, true, false
	}
	return w
}

// nearestPaneRoot is the pane shell or hosted pane process lowest in chain,
// matched by the start time recorded when it was started or adopted. A pane
// with no recorded start time is never a root. An exited shell needs no
// test of its own: once it is reaped its pid names no process with its start
// time, and before that it is a zombie, which has no children and makes no
// call.
func (d *Daemon) nearestPaneRoot(chain []chainLink) (spawnRoot, bool) {
	if len(chain) == 0 {
		return spawnRoot{}, false
	}
	best, found := len(chain), spawnRoot{}
	match := func(kind spawnKind, pid int, start uint64, id string) {
		if pid <= 0 || start == 0 {
			return
		}
		for i := range chain[:best] {
			if chain[i].pid == pid && chain[i].start == start {
				best, found = i, spawnRoot{kind: kind, pid: pid, id: id}
				return
			}
		}
	}
	for _, sess := range d.manager.AllSessions() {
		sess.ptysMu.RLock()
		for _, pty := range sess.ptys {
			// proc and procStart are set under ptysMu (createPTY,
			// adoptPTY), before anything else reads them.
			if pty.proc != nil {
				match(spawnPane, pty.proc.Pid, pty.procStart, pty.windowID)
			}
		}
		sess.ptysMu.RUnlock()
	}
	d.hostedPanesMu.Lock()
	for id, hp := range d.hostedPanes {
		if hp.cmd != nil && hp.cmd.Process != nil {
			match(spawnHosted, hp.cmd.Process.Pid, hp.procStart, id)
		}
	}
	d.hostedPanesMu.Unlock()
	return found, best < len(chain)
}

// processStart is the start time of a process the daemon just started or
// adopted, 0 when it cannot be read.
func processStart(p *os.Process) uint64 {
	if p == nil {
		return 0
	}
	st, _ := procinfo.StartTime(p.Pid)
	return st
}
