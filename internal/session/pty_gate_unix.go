//go:build !windows && !js

package session

import (
	"errors"
	"fmt"
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"golang.org/x/sys/unix"
)

// readGate lets a pane's reader wait for output without holding the read lock.
// The reader blocks on the master and a wake pipe, outside readMu, and reads
// only once the master has something. A blocking read could not be waited
// out: on both platforms the master xpty hands over is in blocking mode, so a
// read deadline is silently ignored (docs/design/live-handoff.md section 2.4),
// and a reader parked in read() would hold readMu for as long as the program
// stays quiet.
//
// The wake pipe is how Close gets a reader out of the wait. Closing the master
// does not wake a wait on it, on either platform.
type readGate struct {
	wakeR, wakeW *os.File
	// waiter blocks until the master or the wake pipe is readable, and
	// reports whether the wake pipe was. It is the platform's (gateWaiter);
	// a test puts a failing one in its place.
	waiter  func(master, wake int) (woken bool, err error)
	release func()
}

// newReadGate returns the gate for a pane whose master is a descriptor here,
// and nil for any other pane, or when the gate cannot be built: such a pane
// reads as before and cannot be quiesced.
func newReadGate(pty paneIO) *readGate {
	r, w, err := os.Pipe()
	if err != nil {
		return nil
	}
	g := &readGate{wakeR: r, wakeW: w}
	ptyspawn.ControlMaster(pty, func(master int) {
		g.control(func(wake int) {
			g.waiter, g.release, err = newGateWaiter(master, wake)
		})
	})
	if g.waiter == nil || err != nil {
		_ = r.Close()
		_ = w.Close()
		return nil
	}
	return g
}

// control runs fn with the wake pipe's read descriptor, held open for the
// length of the call.
func (g *readGate) control(fn func(wake int)) {
	conn, err := g.wakeR.SyscallConn()
	if err != nil {
		return
	}
	_ = conn.Control(func(wake uintptr) { fn(int(wake)) })
}

// wait blocks until the master has output or has hung up, and reports false
// when the gate was woken instead, or the master is already closed.
//
// A wait that fails says nothing about the master, so wait returns the error
// rather than either answer: reporting output would send the reader into a
// blocking read under readMu that a quiesce cannot wait out, and reporting a
// wake would end the pane's output for good.
//
// The wait runs inside Control, which holds the master's descriptor open for
// the length of the call: a Close racing it cannot hand the number to another
// file while the wait still watches it.
func (g *readGate) wait(pty paneIO) (bool, error) {
	woken := true
	var waitErr error
	ran := ptyspawn.ControlMaster(pty, func(master int) {
		g.control(func(wake int) {
			for {
				woken, waitErr = g.waiter(master, wake)
				if !errors.Is(waitErr, unix.EINTR) {
					return
				}
			}
		})
	})
	if waitErr != nil {
		return false, fmt.Errorf("waiting for the pane's output: %w", waitErr)
	}
	return ran && !woken, nil
}

// wake gets a waiting reader out of its wait for good. Close calls it.
func (g *readGate) wake() {
	_, _ = g.wakeW.Write([]byte{0})
}

// close releases the wake pipe and the waiter. The reader calls it on its way
// out.
func (g *readGate) close() {
	g.release()
	_ = g.wakeR.Close()
	_ = g.wakeW.Close()
}
