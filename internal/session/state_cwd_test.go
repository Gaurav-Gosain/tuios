package session

import (
	"testing"
)

// TestTheDirectoryReadIsThrottled guards the cost. GetState is on the render
// path, and reading a process directory is a syscall per window, so a second
// call inside the interval must reuse the first one's answer rather than going
// back to the operating system.
func TestTheDirectoryReadIsThrottled(t *testing.T) {
	sess, id := sessionWithInheritCwd(t, false)
	_ = cwdOfWindow(t, sess, id)

	first := sess.liveCwds()
	sess.cwdCacheMu.Lock()
	at := sess.cwdReadAt
	sess.cwdCacheMu.Unlock()

	second := sess.liveCwds()
	sess.cwdCacheMu.Lock()
	again := sess.cwdReadAt
	sess.cwdCacheMu.Unlock()

	if !at.Equal(again) {
		t.Error("a second read inside the interval went back to the operating system")
	}
	if len(first) != len(second) {
		t.Errorf("the throttled read answered differently: %d then %d", len(first), len(second))
	}
}
