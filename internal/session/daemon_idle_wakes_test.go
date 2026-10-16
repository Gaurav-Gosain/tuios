package session

import (
	"sync/atomic"
	"testing"
	"time"
)

// The daemon's idle budget: a daemon whose panes sit at a prompt arms no
// timer. Each background loop that polled on a ticker sleeps on a latch
// instead (idle_wakes.go), and this test counts their timer fires over an idle
// window once the daemon has settled. Before, the agent monitor woke every
// detection interval and each session's saver every 2 seconds, for ever.
//
// It also checks the other half: activity in a pane wakes the loops again.
// See docs/perf.md, "2026-10 the daemon at idle".

// idleCounters are the timer fires the budget counts.
type idleCounters struct {
	name string
	n    *atomic.Int64
}

// waitIdleSettled waits until no counter moves for settle, and fails when
// that has not happened within limit.
func waitIdleSettled(t *testing.T, counters []idleCounters, settle, limit time.Duration) {
	t.Helper()
	snap := func() []int64 {
		out := make([]int64, len(counters))
		for i, c := range counters {
			out[i] = c.n.Load()
		}
		return out
	}
	deadline := time.Now().Add(limit)
	last, since := snap(), time.Now()
	for time.Since(since) < settle {
		if time.Now().After(deadline) {
			t.Fatalf("the daemon's loops did not settle within %v: %v", limit, last)
		}
		time.Sleep(settle / 10)
		if now := snap(); !equalInt64s(now, last) {
			last, since = now, time.Now()
		}
	}
}

func equalInt64s(a, b []int64) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDaemonIdleArmsNoTimers(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	prevBound := agentDetectQuietBound
	agentDetectQuietBound = 300 * time.Millisecond
	t.Cleanup(func() { agentDetectQuietBound = prevBound })
	const interval = 100 * time.Millisecond

	d, _ := startTestDaemonWith(t, &DaemonConfig{AgentDetectInterval: interval})
	sess, err := d.manager.CreateSession("idle", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	win, err := sess.AddDaemonWindow("Window", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	counters := []idleCounters{
		{"agent monitor", &d.wakes.detectTicks},
		{"session saver", &sess.saverWakes},
	}

	// The shell prints its prompt and the window's creation is saved, which
	// takes up to resurrectionDirtyInterval. Then the daemon has nothing to
	// do.
	waitIdleSettled(t, counters, resurrectionDirtyInterval+5*interval, 20*time.Second)
	before := make([]int64, len(counters))
	for i, c := range counters {
		before[i] = c.n.Load()
	}
	time.Sleep(20 * interval)
	for i, c := range counters {
		if n := c.n.Load() - before[i]; n != 0 {
			t.Errorf("the %s woke %d times in %v of idle, want 0", c.name, n, 20*interval)
		}
	}

	// Typing into the pane wakes the agent monitor again.
	pty := sess.GetPTY(win.PTYID)
	if pty == nil {
		t.Fatal("the window has no PTY")
	}
	woke := d.wakes.detectTicks.Load()
	if _, err := pty.Write([]byte("echo awake\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for d.wakes.detectTicks.Load() == woke {
		if time.Now().After(deadline) {
			t.Fatal("input in a pane did not wake the agent monitor")
		}
		time.Sleep(interval / 2)
	}
}
