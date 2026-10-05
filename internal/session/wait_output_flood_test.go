package session

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// TestWaitForOutputDoesNotSlowFlood is a perf budget. A pending wait-for
// window-output used to capture the whole scrollback on every output event,
// one per PTY read, under the pane's emulator lock. The pane fed the emulator
// behind those captures, and a seq flood took over five times as long while a
// waiter that never matched was pending. The wait now captures at most once
// per waitOutputMinGap.
//
// The budget is on what the waiter does during the flood: how many captures
// it takes and how long they hold the emulator lock, against how long the
// flood ran. Both scale with the machine's load the way the flood does. The
// wall time of a flood with a waiter against one without does not: on a
// shared runner the load changes between the two, and that ratio swung from
// 0.8x to 2.8x with no change in the waiter.
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

	var (
		mu       sync.Mutex
		captures int
		held     time.Duration
	)
	hook := func(d time.Duration) {
		mu.Lock()
		captures++
		held += d
		mu.Unlock()
	}
	waitOutputCaptured.Store(&hook)
	t.Cleanup(func() { waitOutputCaptured.Store(nil) })

	start := time.Now()
	with := best(lines)
	ran := time.Since(start)
	mu.Lock()
	gotCaptures, gotHeld := captures, held
	mu.Unlock()
	t.Logf("seq 1 %d: %v without a waiter, %v with one (%.2fx); the waiter took %d captures holding the lock %v in %v",
		lines, without, with, float64(with)/float64(without), gotCaptures, gotHeld, ran)

	// The gap allows one capture per waitOutputMinGap, and the backstop one
	// more per waitOutputRecheck. A capture per event took about one per
	// millisecond.
	if limit := int(ran/waitOutputMinGap+ran/waitOutputRecheck) + 3; gotCaptures > limit {
		t.Errorf("the waiter took %d captures in %v, more than the %d its gap allows", gotCaptures, ran, limit)
	}
	// A capture per event held the lock for most of the flood. With the gap,
	// the share is a capture's cost against the gap after it, which stays
	// under half until one capture takes as long as the gap.
	if gotHeld > ran/2 {
		t.Errorf("the waiter held the emulator lock %v of the %v the floods ran", gotHeld, ran)
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
