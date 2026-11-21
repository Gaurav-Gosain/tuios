package session

import (
	"testing"
)

// TestAgentDetectSweepIsIdempotentWhenIdle is the invariant the benchmark above
// is measuring against, and the one worth defending: a second sweep over an
// unchanged session must find nothing to change. If it ever reports work on a
// steady session, the daemon is republishing state to every attached client
// every two seconds for no reason, and the benchmark would be measuring that
// rather than the idle floor.
func TestAgentDetectSweepIsIdempotentWhenIdle(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, id)
	matcher := newAgentMatcher(nil)
	resolve := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{
		comm: "bash", argv: []string{"bash"}, exe: "/usr/bin/bash",
	}, true}})

	sess.applyAgentDetection(resolve, matcher.identifyDetail)
	for i := range 3 {
		if n := sess.applyAgentDetection(resolve, matcher.identifyDetail); n != 0 {
			t.Errorf("sweep %d over an unchanged session reported %d changes, want 0", i+2, n)
		}
	}
}
