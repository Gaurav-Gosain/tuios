//go:build !windows && !js

package session

import (
	"errors"
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"golang.org/x/sys/unix"
)

type readGate struct {
	wakeR, wakeW *os.File
	waiter       func(master, wake int) (woken bool, err error)
	release      func()
}

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

func (g *readGate) control(fn func(wake int)) {
	conn, err := g.wakeR.SyscallConn()
	if err != nil {
		return
	}
	_ = conn.Control(func(wake uintptr) { fn(int(wake)) })
}

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
		return false, errfmt.Errorf("waiting for the pane's output: %w", waitErr)
	}
	return ran && !woken, nil
}

func (g *readGate) wake() {
	_, _ = g.wakeW.Write([]byte{0})
}

func (g *readGate) close() {
	g.release()
	_ = g.wakeR.Close()
	_ = g.wakeW.Close()
}
