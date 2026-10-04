package session

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestWaitForOutputDoesNotSlowFlood is a perf budget. A pending wait-for
// window-output used to capture the whole scrollback on every output event,
// one per PTY read, under the pane's emulator lock. The pane fed the emulator
// behind those captures, and a seq flood took over five times as long while a
// waiter that never matched was pending. The wait now captures at most once
// per waitOutputMinGap, so the flood must take about as long with the waiter
// as without it.
//
// The flood is timed to the shell finishing (it touches a file) and the
// emulator applying everything read, not by a capture, so the measurement
// adds no capture of its own.
func TestWaitForOutputDoesNotSlowFlood(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "flood")
	id := sess.GetState().Windows[0].ID
	pty, err := d.resolvePTYForTarget(sess, id)
	if err != nil {
		t.Fatalf("resolvePTYForTarget: %v", err)
	}
	c := dialVerb(t, sp)
	dir := t.TempDir()

	n := 0
	flood := func(lines int) time.Duration {
		t.Helper()
		n++
		marker := filepath.Join(dir, "done-"+strconv.Itoa(n))
		cmd := "seq 1 " + strconv.Itoa(lines) + "; touch " + marker + `\n`
		start := time.Now()
		result(t, c.call(t, `{"id":`+strconv.Itoa(100+n)+`,"verb":"send-text","params":{"session":"flood","window":"`+id+`","text":"`+cmd+`"}}`))
		deadline := start.Add(120 * time.Second)
		for {
			if _, err := os.Stat(marker); err == nil && caughtUp(pty) {
				return time.Since(start)
			}
			if time.Now().After(deadline) {
				t.Fatalf("flood %d never finished", n)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	best := func(lines int) time.Duration {
		a, b := flood(lines), flood(lines)
		return min(a, b)
	}

	// Fill the scrollback first, which is what makes one capture expensive.
	flood(20000)
	const lines = 500000
	without := best(lines)

	// A waiter on its own connection that never matches. Only the request is
	// written: the answer comes when the daemon stops.
	w := dialVerb(t, sp)
	w.send(t, `{"id":1,"verb":"wait-for","params":{"condition":"window-output","session":"flood","window":"`+id+`","pattern":"NEVER-MATCHES-[X]YZ","timeout":600000}}`)
	waitForSubscribers(t, d, 1)

	with := best(lines)
	t.Logf("seq 1 %d: %v without a waiter, %v with one (%.2fx)", lines, without, with, float64(with)/float64(without))
	// Room for a shared machine. A capture per event cost about 3x here, and
	// 5x on the real binary with a wider pane.
	if with > without*3/2+100*time.Millisecond {
		t.Fatalf("a pending wait-for made the flood %.2fx slower (%v against %v)", float64(with)/float64(without), with, without)
	}
}

// caughtUp reports whether the pane's emulator has applied every byte the
// pane has read. The read loop queues several megabytes ahead of the
// emulator, so the shell finishing says nothing about the emulator, and the
// emulator is what a waiter's captures slow down.
func caughtUp(p *PTY) bool {
	p.outputMu.Lock()
	read := p.outputSeq
	p.outputMu.Unlock()
	p.terminalMu.RLock()
	applied := p.vtSeq
	p.terminalMu.RUnlock()
	return applied >= read
}

// waitForSubscribers waits until the event hub has at least n subscribers.
func waitForSubscribers(t *testing.T, d *Daemon, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	count := func() int {
		d.events.mu.Lock()
		defer d.events.mu.Unlock()
		return len(d.events.subs)
	}
	for count() < n {
		if time.Now().After(deadline) {
			t.Fatalf("the waiter never subscribed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
