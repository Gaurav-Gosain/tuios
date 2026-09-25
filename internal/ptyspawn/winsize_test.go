package ptyspawn

import "testing"

type resizeOnly struct{ resized [2]int }

func (r *resizeOnly) Resize(cols, rows int) error {
	r.resized = [2]int{cols, rows}
	return nil
}

type withPixels struct {
	resizeOnly
	winsize [4]int
}

func (w *withPixels) SetWinsize(cols, rows, xpixel, ypixel int) error {
	w.winsize = [4]int{cols, rows, xpixel, ypixel}
	return nil
}

func TestSetWinsizeUsesOneCall(t *testing.T) {
	px := &withPixels{}
	if err := SetWinsize(px, 80, 24, 800, 480); err != nil {
		t.Fatal(err)
	}
	if px.winsize != [4]int{80, 24, 800, 480} {
		t.Errorf("SetWinsize wrote %v, want [80 24 800 480]", px.winsize)
	}
	if px.resized != [2]int{} {
		t.Errorf("SetWinsize also called Resize(%v), which is a second winsize write", px.resized)
	}

	cells := &resizeOnly{}
	if err := SetWinsize(cells, 80, 24, 800, 480); err != nil {
		t.Fatal(err)
	}
	if cells.resized != [2]int{80, 24} {
		t.Errorf("a pty without pixels was resized to %v, want [80 24]", cells.resized)
	}
}
