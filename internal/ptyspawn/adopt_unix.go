//go:build !windows && !js

package ptyspawn

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// controller is a PTY that hands out its master descriptor for the length of
// one call. xpty.UnixPty and AdoptedPty are; ConPTY and a pane whose bytes
// come from another machine are not.
type controller interface {
	Control(fn func(fd uintptr)) error
}

// ControlMaster runs fn with the master descriptor of p, and reports false
// when p has none. The descriptor stays open for as long as fn runs, even
// against a concurrent Close, which is what lets a reader block on it.
//
// It never calls Fd: on an *os.File that switches the descriptor to blocking
// mode for good, and a reader that relies on readiness must not find that
// state changed under it.
func ControlMaster(p any, fn func(fd int)) bool {
	c, ok := p.(controller)
	if !ok {
		return false
	}
	ran := false
	err := c.Control(func(fd uintptr) {
		ran = true
		fn(int(fd))
	})
	return err == nil && ran
}

// AdoptedPty is the master side of a PTY this process inherited across an
// exec, with no slave copy: the program on the slave holds the only one, so
// the master reads end of file once it is gone.
type AdoptedPty struct {
	master    *os.File
	slaveName string
}

// Adopt wraps an inherited master descriptor. slaveName is the path of the
// terminal the program runs on, which the descriptor cannot tell.
func Adopt(fd int, slaveName string) (*AdoptedPty, error) {
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err != nil {
		return nil, fmt.Errorf("adopt pty fd %d: %w", fd, err)
	}
	// The descriptor stops being inherited by anything this process starts
	// from here on, as every descriptor Go opens does.
	unix.CloseOnExec(fd)
	return &AdoptedPty{master: os.NewFile(uintptr(fd), "/dev/ptmx"), slaveName: slaveName}, nil
}

// Read reads the program's output.
func (p *AdoptedPty) Read(b []byte) (int, error) { return p.master.Read(b) }

// Write writes the program's input.
func (p *AdoptedPty) Write(b []byte) (int, error) { return p.master.Write(b) }

// Close closes the master.
func (p *AdoptedPty) Close() error { return p.master.Close() }

// SlaveName is the path of the terminal the program runs on.
func (p *AdoptedPty) SlaveName() string { return p.slaveName }

// Control runs fn with the master descriptor.
func (p *AdoptedPty) Control(fn func(fd uintptr)) error {
	conn, err := p.master.SyscallConn()
	if err != nil {
		return err
	}
	return conn.Control(fn)
}

// Resize sets the size in cells, with no pixels.
func (p *AdoptedPty) Resize(width, height int) error {
	return p.SetWinsize(width, height, 0, 0)
}

// SetWinsize sets the size in cells and in pixels with one ioctl.
func (p *AdoptedPty) SetWinsize(width, height, xpixel, ypixel int) error {
	var rErr error
	if err := p.Control(func(fd uintptr) {
		rErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{
			Row:    uint16(height), //nolint:gosec // a pane size, far below 65536
			Col:    uint16(width),  //nolint:gosec
			Xpixel: uint16(xpixel), //nolint:gosec
			Ypixel: uint16(ypixel), //nolint:gosec
		})
	}); err != nil {
		return err
	}
	return rErr
}
