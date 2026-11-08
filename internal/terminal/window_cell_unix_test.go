//go:build unix

package terminal

import (
	"testing"

	"golang.org/x/sys/unix"
)

// TestAStandaloneWindowIsBornWithTheHostCell is a deterministic race
// regression. A standalone window's pty was made with the fallback cell's
// pixels, and the host's cell was set right after its shell started, which
// changed the winsize in its pixels alone. The kernel compares the whole
// struct, so it sent the shell a SIGWINCH at the size it started with. The
// e2e (TestNewPaneShellStartsAtItsSize) sees that only when the shell's trap
// is set before the second write, which is now and then. This reads the
// winsize at the point the shell may first read it: as NewWindowInCell
// returns, before anything else writes it.
//
// Negative control: with NewWindowInCell's SpawnTTY and its post-start
// SetWinsize both given a cell of 0 x 0, as before, the pty holds 400 x 200
// pixels (the fallback cell) here and the test fails every time.
func TestAStandaloneWindowIsBornWithTheHostCell(t *testing.T) {
	const cellW, cellH = 9, 19 // not the fallback cell (10 x 20)
	exit := make(chan string, 4)
	data := make(chan struct{}, 64)
	w, err := NewWindowInCell("", "cell-test", "cell", 0, 0, 42, 12, 0, exit, data, 100, cellW, cellH, "/bin/sh", "-c", "sleep 30")
	if err != nil {
		t.Fatalf("NewWindowInCell: %v", err)
	}
	t.Cleanup(func() { w.Close() })
	fd, ok := w.Pty.(interface{ Fd() uintptr })
	if !ok {
		t.Skip("the pty has no file descriptor to read")
	}
	ws, err := unix.IoctlGetWinsize(int(fd.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		t.Fatalf("TIOCGWINSZ: %v", err)
	}
	cols, rows := 42-2, 12-2
	if int(ws.Col) != cols || int(ws.Row) != rows || int(ws.Xpixel) != cols*cellW || int(ws.Ypixel) != rows*cellH {
		t.Fatalf("the pty was born at %dx%d cells and %dx%d pixels, want %dx%d cells and %dx%d pixels (the host's %dx%d cell)",
			ws.Col, ws.Row, ws.Xpixel, ws.Ypixel, cols, rows, cols*cellW, rows*cellH, cellW, cellH)
	}
	// Setting the same cell after the start writes the same winsize.
	w.SetCellPixelDimensions(cellW, cellH)
	again, err := unix.IoctlGetWinsize(int(fd.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		t.Fatalf("TIOCGWINSZ: %v", err)
	}
	if *again != *ws {
		t.Fatalf("SetCellPixelDimensions with the cell the pty was born with changed the winsize from %+v to %+v", *ws, *again)
	}
}
