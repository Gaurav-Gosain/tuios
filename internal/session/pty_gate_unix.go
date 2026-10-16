//go:build !windows && !js

package session

import (
	"errors"
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"golang.org/x/sys/unix"
)

// readGate lets a pane's reader wait for output without holding the read lock.
// The reader blocks in poll on the master and a wake pipe, outside readMu, and
// reads only once the master has something. A blocking read could not be
// waited out: on both platforms the master xpty hands over is in blocking
// mode, so a read deadline is silently ignored (docs/design/live-handoff.md
// section 2.4), and a reader parked in read() would hold readMu for as long as
// the program stays quiet.
//
// The wake pipe is how Close gets a reader out of poll. Closing the master
// does not wake a poll on it, on either platform.
type readGate struct {
	wakeR, wakeW *os.File
}

// newReadGate returns the gate for a pane whose master is a descriptor here,
// and nil for any other pane.
func newReadGate(pty paneIO) *readGate {
	if !ptyspawn.ControlMaster(pty, func(int) {}) {
		return nil
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil
	}
	return &readGate{wakeR: r, wakeW: w}
}

// wait blocks until the master has output or has hung up, and reports false
// when the gate was woken instead, or the master is already closed.
//
// The poll runs inside Control, which holds the master's descriptor open for
// the length of the call: a Close racing it cannot hand the number to another
// file while poll still watches it.
func (g *readGate) wait(pty paneIO) bool {
	woken := false
	ran := ptyspawn.ControlMaster(pty, func(master int) {
		conn, err := g.wakeR.SyscallConn()
		if err != nil {
			woken = true
			return
		}
		_ = conn.Control(func(wake uintptr) {
			fds := []unix.PollFd{
				{Fd: int32(master), Events: unix.POLLIN}, //nolint:gosec // a descriptor number
				{Fd: int32(wake), Events: unix.POLLIN},   //nolint:gosec
			}
			for {
				_, err := unix.Poll(fds, -1)
				if errors.Is(err, unix.EINTR) {
					continue
				}
				break
			}
			woken = fds[1].Revents != 0
		})
	})
	return ran && !woken
}

// wake gets a waiting reader out of poll for good. Close calls it.
func (g *readGate) wake() {
	_, _ = g.wakeW.Write([]byte{0})
}

// close releases the wake pipe. The reader calls it on its way out.
func (g *readGate) close() {
	_ = g.wakeR.Close()
	_ = g.wakeW.Close()
}
