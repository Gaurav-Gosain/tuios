package session

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// BenchmarkAgentDetectScanLockWait measures what the detection poll costs the
// rest of a session: how long a writer that wants the session's state lock
// waits while scans run, over real panes running real shells.
//
// The scan reads the foreground process of every pane, which on darwin is three
// sysctls a pane and on Linux a few procfs reads. It used to do those reads
// while holding the state write lock, so every writer in the session (a state
// sync from a client, a set-agent-state, a window closing) queued behind them
// every two seconds. The scan now reads with no lock held and takes the write
// lock only to apply a change, so an idle session's writers never wait on it.
//
// Reported per scan: ns/op is the whole scan, max-wait-us is the longest wait a
// writer polling the lock saw while the scans ran, which is about one scan's
// hold of the write lock, and mean-wait-us is its average over every poll. Every pane is
// scanned on every op (applyAgentDetection has no backoff), so the numbers
// compare full scans before and after. Measured figures are in docs/perf.md.
func BenchmarkAgentDetectScanLockWait(b *testing.B) {
	for _, panes := range []int{8, 32} {
		b.Run(fmt.Sprintf("panes=%d", panes), func(b *testing.B) {
			b.Cleanup(useResurrectionDir(b.TempDir()))
			sess, err := NewSession("lockwait", &SessionConfig{}, 80, 24)
			if err != nil {
				b.Fatalf("NewSession: %v", err)
			}
			b.Cleanup(sess.Stop)
			for range panes {
				if _, err := sess.AddDaemonWindow("Window", nil); err != nil {
					b.Fatalf("AddDaemonWindow: %v", err)
				}
			}
			// Let the shells start, so the scan reads live processes.
			time.Sleep(time.Second)

			resolve := func(ptyID string) (foregroundInfo, bool) {
				pty := sess.GetPTY(ptyID)
				if pty == nil {
					return foregroundInfo{}, false
				}
				pid := pty.ShellPID()
				info, running := foregroundProcess(pid)
				info.shellPID = pid
				return info, running
			}
			identify := newAgentMatcher(nil).identifyDetail
			// One scan first, so the labels and shell pids the first scan
			// writes are not counted as an idle scan's cost.
			sess.applyAgentDetection(resolve, identify)

			stop := make(chan struct{})
			var wg sync.WaitGroup
			var maxWait, sumWait time.Duration
			var waits int
			wg.Go(func() {
				for {
					select {
					case <-stop:
						return
					default:
					}
					t0 := time.Now()
					sess.stateMu.Lock()
					wait := time.Since(t0)
					sess.stateMu.Unlock()
					sumWait += wait
					waits++
					if wait > maxWait {
						maxWait = wait
					}
					time.Sleep(50 * time.Microsecond)
				}
			})

			// Scans are spaced apart, as the real two-second tick spaces them.
			// Run back to back, a scan could take the lock again before the
			// waiting writer got it, and the wait would measure several scans.
			b.ResetTimer()
			for range b.N {
				sess.applyAgentDetection(resolve, identify)
				b.StopTimer()
				time.Sleep(2 * time.Millisecond)
				b.StartTimer()
			}
			b.StopTimer()
			close(stop)
			wg.Wait()
			if waits > 0 {
				b.ReportMetric(float64(maxWait.Microseconds()), "max-wait-us")
				b.ReportMetric(float64(sumWait.Microseconds())/float64(waits), "mean-wait-us")
			}
		})
	}
}
