//go:build ghostty

package vt

import (
	"testing"
)

func TestGhosttyAltBufferIsBuiltOnFirstSwitch(t *testing.T) {
	g := NewGhosttyTerminal(80, 24)
	defer g.Close()
	if g.bufs[1] != nil {
		t.Fatal("a fresh terminal already carries an alternate-screen buffer")
	}
	g.Resize(100, 30)
	if g.bufs[1] != nil {
		t.Fatal("a resize built the alternate-screen buffer")
	}
	_, _ = g.Write([]byte("\x1b[?1049h\x1b[5;10Halt-screen-text"))
	if !g.IsAltScreen() {
		t.Fatal("not on the alternate screen after 1049h")
	}
	c := g.CellAt(9, 4)
	if c == nil || c.Content != "a" {
		t.Fatalf("cell (9,4) on the alternate screen is %#v, want the text written there", c)
	}
	if g.bufs[1] == nil || g.bufs[1].Width() != 100 || g.bufs[1].Height() != 30 {
		t.Fatalf("alternate buffer is %v, want 100x30", g.bufs[1])
	}
}
