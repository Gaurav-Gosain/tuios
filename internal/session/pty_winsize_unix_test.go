//go:build !windows

package session

import (
	"testing"

	"golang.org/x/sys/unix"
)

// readWinsize reads the real PTY's window size, as a guest would.
func readWinsize(t *testing.T, p *PTY) *unix.Winsize {
	t.Helper()
	fd, ok := p.pty.(interface{ Fd() uintptr })
	if !ok {
		t.Skip("the pane has no local pty")
	}
	ws, err := unix.IoctlGetWinsize(int(fd.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		t.Fatalf("TIOCGWINSZ: %v", err)
	}
	return ws
}

// A resize carries the pixel size in the same winsize as the cells, so a
// kitty graphics or sixel guest never reads a size with zero pixels in it.
// Resize used to write zero pixels and rely on the UpdatePixelDimensions call
// after it to put them back, which was a second ioctl and a second SIGWINCH.
func TestPTYResizeKeepsThePixelSize(t *testing.T) {
	_, sess := newTestDaemonSession(t)
	pty, err := sess.CreatePTY("win-winsize", 40, 12, func(string) {})
	if err != nil {
		t.Fatalf("CreatePTY failed: %v", err)
	}

	if err := pty.UpdatePixelDimensions(9, 18); err != nil {
		t.Fatalf("UpdatePixelDimensions: %v", err)
	}
	ws := readWinsize(t, pty)
	if ws.Col != 40 || ws.Row != 12 || ws.Xpixel != 40*9 || ws.Ypixel != 12*18 {
		t.Fatalf("after UpdatePixelDimensions winsize = %dx%d %dx%dpx, want 40x12 360x216px",
			ws.Col, ws.Row, ws.Xpixel, ws.Ypixel)
	}

	if err := pty.Resize(50, 10); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	ws = readWinsize(t, pty)
	if ws.Col != 50 || ws.Row != 10 {
		t.Fatalf("after Resize winsize is %dx%d cells, want 50x10", ws.Col, ws.Row)
	}
	if ws.Xpixel != 50*9 || ws.Ypixel != 10*18 {
		t.Errorf("ASSERTION: Resize left the pixel size at %dx%d, want %dx%d: the guest sees a size with no pixels",
			ws.Xpixel, ws.Ypixel, 50*9, 10*18)
	}

	// A cell size change on its own still reaches the PTY.
	if err := pty.UpdatePixelDimensions(10, 20); err != nil {
		t.Fatalf("UpdatePixelDimensions: %v", err)
	}
	ws = readWinsize(t, pty)
	if ws.Xpixel != 500 || ws.Ypixel != 200 {
		t.Errorf("after a cell size change the pixel size is %dx%d, want 500x200", ws.Xpixel, ws.Ypixel)
	}
}
