package session

// What the daemon costs when nobody is doing anything.
//
// The client's idle cost is a defended invariant: BenchmarkIdleTick pins it at
// zero work and zero renders per tick, and a change that moves it is reverted.
// The daemon has no such guard, and unlike the client it is not driven by
// output at all: agentMonitor (daemon.go) runs on a 2 second ticker whatever
// the panes are doing, and every tick walks every session's every window,
// resolves each one's foreground process, and does it inside the session state
// lock.
//
// That makes the daemon's floor O(panes) per two seconds, forever, on a machine
// where every pane is a shell sitting at a prompt. These benchmarks price the
// two halves of it separately, because they scale differently and only one of
// them is Go code: the walk itself, and one process resolution, which on Linux
// is four reads out of procfs.
//
// Benchmark only. The pane counts build real PTYs, so the setup is the
// expensive part; keep the counts to the ones that answer the question.

import (
	"fmt"
	"testing"
)

// benchSession builds a session holding n real daemon windows, which is what
// the monitor walks.
func benchSession(tb testing.TB, n int) (*Session, []string) {
	tb.Helper()
	tb.Cleanup(useResurrectionDir(tb.TempDir()))
	sess, err := NewSession("idle-bench", &SessionConfig{}, 80, 24)
	if err != nil {
		tb.Fatalf("NewSession: %v", err)
	}
	tb.Cleanup(sess.Stop)

	ptyIDs := make([]string, 0, n)
	for i := range n {
		if _, err := sess.AddDaemonWindow(fmt.Sprintf("w%d", i), nil); err != nil {
			tb.Fatalf("AddDaemonWindow: %v", err)
		}
	}
	for _, w := range sess.GetState().Windows {
		if w.PTYID != "" {
			ptyIDs = append(ptyIDs, w.PTYID)
		}
	}
	return sess, ptyIDs
}

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
