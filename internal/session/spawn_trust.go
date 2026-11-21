package session

import (
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

type spawnKind uint8

const (
	spawnDaemon spawnKind = iota + 1
	spawnPane
	spawnHosted
	spawnHook
)

type spawnRoot struct {
	kind spawnKind
	pid  int
	id   string
}

type spawnWalk struct {
	root     spawnRoot
	found    bool
	broken   bool
	readable bool
	tty      int64
}

type procLinkFunc func(pid int) (ppid int, tty int64, start uint64, ok bool)

func (d *Daemon) procLink() procLinkFunc {
	if fake := d.hookProc; fake != nil {
		return func(pid int) (int, int64, uint64, bool) {
			ppid, start, ok := fake(pid)
			return ppid, 0, start, ok
		}
	}
	return readProcLink
}

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

type chainLink struct {
	pid   int
	start uint64
}

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
		if !d.sharedProcess && cur == d.selfPID {
			w.root, w.found = spawnRoot{kind: spawnDaemon, pid: cur}, true
			break
		}
		if n > 0 {
			if cur == 1 {
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

func processStart(p *os.Process) uint64 {
	if p == nil {
		return 0
	}
	st, _ := procinfo.StartTime(p.Pid)
	return st
}
