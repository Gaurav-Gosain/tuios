//go:build linux || darwin

package session

import (
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

// TestHookOfPeerMatchesTheStartTime: a caller is a hook only when a process
// in its ancestry is a hook shell the daemon started, with the start time the
// daemon recorded. A pid that names another process now, which a reused pid
// does, is not trusted, and a parent that started after the process below it
// ends the walk, since the chain past it is not the caller's.
//
// A reused pid cannot be made on purpose, so the registry and the process
// reads are given here: a start time other than the live one stands for a
// pid that was reused.
//
// The negative controls are in e2e/tui/NEGATIVE_CONTROLS.md, "The owners of
// hook and herdr plugin surfaces".
func TestHookOfPeerMatchesTheStartTime(t *testing.T) {
	// The trailing : keeps sh from running sleep in its own place, so
	// sleep is a child of the shell.
	shell := exec.Command("sh", "-c", "sleep 30; :")
	if err := shell.Start(); err != nil {
		t.Fatal(err)
	}
	a := shell.Process.Pid
	waited := make(chan struct{})
	go func() {
		_ = shell.Wait()
		close(waited)
	}()
	b := 0
	t.Cleanup(func() {
		if b > 0 {
			_ = syscall.Kill(b, syscall.SIGKILL)
		}
		_ = shell.Process.Kill()
		<-waited
	})
	aStart, ok := procinfo.StartTime(a)
	if !ok {
		t.Fatalf("the start time of the shell %d cannot be read", a)
	}
	sid, ok := procinfo.SessionID(a)
	if !ok {
		t.Fatalf("the session of the shell %d cannot be read", a)
	}
	deadline := time.Now().Add(10 * time.Second)
	for b == 0 {
		for _, pid := range procinfo.SessionMembers(sid) {
			if ppid, ok := procinfo.ParentPID(pid); ok && ppid == a {
				b = pid
			}
		}
		if b == 0 {
			if time.Now().After(deadline) {
				t.Fatalf("the shell %d started no sleep", a)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	d := NewDaemon(&DaemonConfig{})
	t.Cleanup(d.manager.Shutdown)
	csB := &connState{clientID: "sleep", peerPID: b}
	d.pinPeer(csB)
	csA := &connState{clientID: "shell", peerPID: a}
	d.pinPeer(csA)
	if !csB.peerStartOK || !csA.peerStartOK {
		t.Fatal("the start times of the shell and the sleep cannot be read")
	}
	bStart := csB.peerStart
	registry := func(pid int, start uint64) func(int) (uint64, bool) {
		return func(p int) (uint64, bool) { return start, p == pid }
	}

	for _, c := range []struct {
		what  string
		cs    *connState
		hooks func(int) (uint64, bool)
		want  bool
	}{
		{"the parent registered with its start time", csB, registry(a, aStart), true},
		{"the parent registered with another start time", csB, registry(a, aStart+1), false},
		{"the caller itself registered with its start time", csA, registry(a, aStart), true},
		{"the caller itself registered with another start time", csA, registry(a, aStart+1), false},
		{"nothing registered", csB, func(int) (uint64, bool) { return 0, false }, false},
	} {
		d.hookStarts = c.hooks
		if got := d.hookOfPeer(c.cs); got != c.want {
			t.Errorf("%s: hookOfPeer = %v, want %v", c.what, got, c.want)
		}
	}

	// The chain above the sleep is given. These pids are above every
	// kernel's pid limit, so no live process holds them.
	const p, h = 1<<22 + 1, 1<<22 + 2
	chain := func(pStart uint64) func(int) (int, uint64, bool) {
		return func(pid int) (int, uint64, bool) {
			switch pid {
			case b:
				return p, bStart, true
			case p:
				return h, pStart, true
			case h:
				return 1, bStart - 2, true
			}
			return 0, 0, false
		}
	}
	for _, c := range []struct {
		what  string
		proc  func(int) (int, uint64, bool)
		hooks func(int) (uint64, bool)
		want  bool
	}{
		{"a parent younger than the caller, registered with that start time", chain(bStart + 1), registry(p, bStart+1), false},
		{"a registered hook above a parent younger than the caller", chain(bStart + 1), registry(h, bStart-2), false},
		{"a registered hook above a parent older than the caller", chain(bStart - 1), registry(h, bStart-2), true},
	} {
		d.hookProc, d.hookStarts = c.proc, c.hooks
		if got := d.hookOfPeer(csB); got != c.want {
			t.Errorf("%s: hookOfPeer = %v, want %v", c.what, got, c.want)
		}
	}

	// Both processes end by their own pids.
	if err := syscall.Kill(b, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if err := shell.Process.Kill(); err != nil {
		t.Logf("the shell had ended: %v", err)
	}
	<-waited
	for _, pr := range []struct {
		pid   int
		start uint64
	}{{a, aStart}, {b, bStart}} {
		deadline := time.Now().Add(10 * time.Second)
		for {
			if st, ok := procinfo.StartTime(pr.pid); !ok || st != pr.start {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("process %d is still there", pr.pid)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}
