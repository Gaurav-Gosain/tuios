//go:build !windows && !js

package session

import (
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"golang.org/x/sys/unix"
)

// TestReadGateNeverReportsOutputAfterAFailedWait holds the read gate to what
// its answer means: true sends the reader into a blocking read under readMu,
// and a quiesce takes readMu, so true must only ever mean the master has
// something to read.
//
// The ways it could go wrong, each a case below:
//   - a wait error other than EINTR reads as output, and the reader blocks in
//     read until the program next prints, holding readMu, so a quiesce of a
//     quiet pane never returns;
//   - the same error reads as a wake, and the pane's output ends for good
//     while its program runs;
//   - EINTR, which is no failure, is passed on instead of retried.
//
// A real wait cannot be made to fail on two valid descriptors, so the failing
// one is put in place of the gate's.
func TestReadGateNeverReportsOutputAfterAFailedWait(t *testing.T) {
	pty, cmd, err := ptyspawn.Spawn(80, 24, func() *exec.Cmd {
		//tuios:allow-exec a test pane that prints once and stays
		return exec.Command("/bin/sh", "-c", "echo ready; exec sleep 30")
	}, func(string, ...any) {})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = pty.Close()
		_ = cmd.Wait()
	})
	g := newReadGate(pty)
	if g == nil {
		t.Fatal("a pane with a master here has no read gate")
	}
	t.Cleanup(g.close)

	// The master has output from here on: nothing reads it.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if ready, err := g.wait(pty); err == nil && ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the pane's output never reached the master")
		}
		time.Sleep(10 * time.Millisecond)
	}

	real := g.waiter
	// Whatever a failed wait says about the wake pipe, it is not an answer.
	for _, woken := range []bool{false, true} {
		t.Run(fmt.Sprintf("a failed wait is neither output nor a wake, woken=%v", woken), func(t *testing.T) {
			g.waiter = func(int, int) (bool, error) { return woken, unix.ENOMEM }
			t.Cleanup(func() { g.waiter = real })
			ready, err := g.wait(pty)
			if ready {
				t.Fatal("ASSERTION: the gate reported output after the wait failed with ENOMEM, so the reader would block in read holding readMu")
			}
			if err == nil {
				t.Fatal("ASSERTION: the gate reported a wake after the wait failed with ENOMEM, so the reader would stop for good")
			}
		})
	}

	t.Run("EINTR is retried", func(t *testing.T) {
		calls := 0
		g.waiter = func(master, wake int) (bool, error) {
			calls++
			if calls == 1 {
				return true, unix.EINTR
			}
			return real(master, wake)
		}
		t.Cleanup(func() { g.waiter = real })
		ready, err := g.wait(pty)
		if err != nil || !ready || calls != 2 {
			t.Fatalf("ASSERTION: after an EINTR the gate answered ready=%v err=%v in %d waits, want ready in 2", ready, err, calls)
		}
	})
}
