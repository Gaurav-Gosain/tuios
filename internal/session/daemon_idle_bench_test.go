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
	"os"
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

// BenchmarkDaemonAgentDetectSweep measures one agentMonitor tick's Go half: the
// walk over every window, the state lock it holds while walking, and the state
// publish it triggers when anything changed.
//
// The resolver is faked so this is the sweep and not the procfs reads, which
// BenchmarkForegroundResolve prices separately. "steady" is the real idle case,
// where nothing has changed since the last tick and the sweep should find
// nothing to do.
func BenchmarkDaemonAgentDetectSweep(b *testing.B) {
	for _, n := range []int{1, 8, 32} {
		b.Run(fmt.Sprintf("panes-%d/steady", n), func(b *testing.B) {
			sess, ptyIDs := benchSession(b, n)
			matcher := newAgentMatcher(nil)
			table := make(map[string]fakeProc, len(ptyIDs))
			for _, id := range ptyIDs {
				// A shell at a prompt: running, and not an agent.
				table[id] = fakeProc{foregroundInfo{
					comm: "bash", argv: []string{"bash"}, exe: "/usr/bin/bash",
				}, true}
			}
			resolve := fakeResolver(table)
			// Settle, so the measured ticks are the steady state rather than
			// the first one that has labels to write.
			sess.applyAgentDetection(resolve, matcher.identify)

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = sess.applyAgentDetection(resolve, matcher.identify)
			}
		})
	}
}

// BenchmarkForegroundResolve prices the other half: what it costs to ask one
// pane what is running in it. This is the part that multiplies by the pane
// count on every tick, and on Linux it is procfs reads rather than Go.
//
// It resolves against this process, which is a real pid with a real /proc
// entry, so the cost is the real one. A daemon pane resolves its shell's
// foreground group first, which is one extra read, so this is a floor.
func BenchmarkForegroundResolve(b *testing.B) {
	pid := os.Getpid()
	b.Run("read-process-info", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = readProcessInfo(pid)
		}
	})
	b.Run("full-foreground", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = foregroundProcess(pid)
		}
	})
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

	sess.applyAgentDetection(resolve, matcher.identify)
	for i := range 3 {
		if n := sess.applyAgentDetection(resolve, matcher.identify); n != 0 {
			t.Errorf("sweep %d over an unchanged session reported %d changes, want 0", i+2, n)
		}
	}
}
