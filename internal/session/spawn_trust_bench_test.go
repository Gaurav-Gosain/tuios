//go:build linux || darwin

package session

import (
	"os"
	"strconv"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

// The cost of placing a caller, against the parent walk it replaced. The
// legacy functions are the walks of paneOriginWith and hookOfPeer before
// spawn_trust.go, kept here only to measure against.

func legacyPaneAncestor(d *Daemon, pid int) bool {
	self := os.Getpid()
	ppid, tty, ok := readProcLineage(pid)
	if !ok {
		return true
	}
	if tty != 0 && d.paneTTYs()[tty] {
		return true
	}
	for range paneOriginMaxDepth {
		if ppid == self {
			return true
		}
		if ppid <= 1 {
			break
		}
		next, _, ok := readProcLineage(ppid)
		if !ok {
			break
		}
		ppid = next
	}
	return false
}

func legacyHookWalk(d *Daemon, pid int, pinned uint64) bool {
	self := os.Getpid()
	below := pinned
	for cur, depth := pid, 0; depth < paneOriginMaxDepth && cur > 1; depth++ {
		if cur == self {
			return false
		}
		ppid, _, ok := readProcLineage(cur)
		if !ok {
			return false
		}
		start, ok := procinfo.StartTime(cur)
		if !ok {
			return false
		}
		if depth == 0 {
			start = pinned
		} else if start > below {
			return false
		}
		if st, ok := d.hooks.HookStart(cur); ok && st == start {
			return true
		}
		cur, below = ppid, start
	}
	return false
}

func newSpawnBenchDaemon(b *testing.B, panes int) (*Daemon, int) {
	b.Helper()
	d, _ := startTestDaemonWith(&testing.T{}, &DaemonConfig{})
	b.Cleanup(d.Stop)
	sess := makeSessionWithWindow(&testing.T{}, d, "bench")
	for i := 1; i < panes; i++ {
		if _, err := sess.AddDaemonWindow("w", nil); err != nil {
			b.Fatal(err)
		}
	}
	pty := sess.GetPTY(sess.GetState().Windows[0].PTYID)
	return d, pty.ShellPID()
}

// BenchmarkPlaceCaller: "person" is the parent of this test binary, outside
// every pane, which walks to init. "pane" is a pane's shell.
func BenchmarkPlaceCaller(b *testing.B) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		b.Skip("no procfs")
	}
	for _, panes := range []int{1, 32} {
		d, shell := newSpawnBenchDaemon(b, panes)
		person := os.Getppid()
		pinned, _ := procinfo.StartTime(person)
		name := func(s string) string { return s + "/panes=" + strconv.Itoa(panes) }
		b.Run(name("paneOrigin-legacy-person"), func(b *testing.B) {
			for b.Loop() {
				legacyPaneAncestor(d, person)
			}
		})
		b.Run(name("paneOrigin-new-person"), func(b *testing.B) {
			for b.Loop() {
				d.paneOriginWith(person, procinfo.Env{}, false)
			}
		})
		b.Run(name("paneOrigin-legacy-pane"), func(b *testing.B) {
			for b.Loop() {
				legacyPaneAncestor(d, shell)
			}
		})
		b.Run(name("paneOrigin-new-pane"), func(b *testing.B) {
			for b.Loop() {
				d.paneOriginWith(shell, procinfo.Env{}, false)
			}
		})
		b.Run(name("hookWalk-legacy-person"), func(b *testing.B) {
			for b.Loop() {
				legacyHookWalk(d, person, pinned)
			}
		})
		b.Run(name("hookWalk-new-person"), func(b *testing.B) {
			for b.Loop() {
				d.spawnRootOf(person, pinned, false)
			}
		})
	}
}
