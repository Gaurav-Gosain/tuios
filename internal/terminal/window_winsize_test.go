package terminal

import (
	"testing"

	xpty "github.com/charmbracelet/x/xpty"

	"tuios.dev/vt"
)

type countingPty struct {
	xpty.Pty
	resizes  int
	winsizes [][4]int
}

func (p *countingPty) Resize(int, int) error { p.resizes++; return nil }

func (p *countingPty) SetWinsize(cols, rows, xpixel, ypixel int) error {
	p.winsizes = append(p.winsizes, [4]int{cols, rows, xpixel, ypixel})
	return nil
}

func (p *countingPty) Fd() uintptr { return ^uintptr(0) }

// A standalone pane resize is one winsize write carrying the pixels. It used
// to be Resize, which writes zero pixels, and then a second raw TIOCSWINSZ
// with the pixels, and each write that changed the struct was a SIGWINCH.
func TestStandaloneResizeWritesTheWinsizeOnce(t *testing.T) {
	fake := &countingPty{}
	w := &Window{
		Tiled:           true,
		Width:           60,
		Height:          40,
		Terminal:        vt.NewEmulator(60, 40),
		Pty:             fake,
		CellPixelWidth:  10,
		CellPixelHeight: 20,
	}

	w.Resize(80, 30)

	if fake.resizes != 0 {
		t.Errorf("ASSERTION: the resize called Resize %d times, which writes zero pixels", fake.resizes)
	}
	want := [4]int{80, 30, 800, 600}
	if len(fake.winsizes) != 1 || fake.winsizes[0] != want {
		t.Errorf("ASSERTION: winsize writes = %v, want exactly one %v", fake.winsizes, want)
	}
}
