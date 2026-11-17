package session

import (
	"fmt"
	"testing"
	"time"
)

// TestLiveFactsPushesKeepTheGap: a pane that flips its OSC 1 name in a loop
// pushes the session's state at most once per liveFactsPushGap, and a push
// still starts after the last flip, so the clients get the last name.
func TestLiveFactsPushesKeepTheGap(t *testing.T) {
	sess := newTestSessionWithWindow(t)
	win := sess.GetState().Windows[0]
	p := sess.GetPTY(win.PTYID)
	if p == nil {
		t.Fatal("the window has no PTY")
	}
	version := func() int {
		sess.stateMu.RLock()
		defer sess.stateMu.RUnlock()
		return sess.state.Version
	}

	before := version()
	start := time.Now()
	flips := 0
	for time.Since(start) < 5*liveFactsPushGap {
		tailShownWrite(p, fmt.Sprintf("\x1b]1;name%d\a", flips))
		flips++
	}
	lastFlip := time.Since(liveFactsEpoch)
	last := fmt.Sprintf("name%d", flips-1)

	eventually(t, "a push starts after the last flip", 2*time.Second, func() bool {
		return !sess.liveFactsPushQueued.Load() && time.Duration(sess.liveFactsPushedAt.Load()) >= lastFlip
	})
	if got := sess.GetState().IconNames[win.ID]; got != last {
		t.Fatalf("the state carries icon name %q, want the last one %q", got, last)
	}
	pushes := version() - before
	// One push per gap over the loop, the trailing one, and one for a shell
	// prompt that lands meanwhile.
	limit := int(time.Since(start)/liveFactsPushGap) + 3
	t.Logf("%d flips, %d pushes, limit %d", flips, pushes, limit)
	if pushes > limit {
		t.Fatalf("%d OSC 1 flips pushed the state %d times, more than %d", flips, pushes, limit)
	}
	if pushes == 0 {
		t.Fatal("the flips pushed nothing")
	}
}
