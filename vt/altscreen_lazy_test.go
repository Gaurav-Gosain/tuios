package vt

import (
	"strings"
	"testing"
)

// The alternate screen is a full grid of 112-byte cells that most panes never
// use, so it is not built until the guest first switches to it.

func altText(e *Emulator, y int) string {
	var b strings.Builder
	for x := range e.Width() {
		if c := e.CellAt(x, y); c != nil {
			b.WriteString(c.Content)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func TestAlternateScreenIsBuiltOnFirstSwitch(t *testing.T) {
	e := NewEmulator(80, 24)
	if w, h := e.scrs[1].buf.Width(), e.scrs[1].buf.Height(); w != 1 || h != 1 {
		t.Fatalf("a fresh emulator's alternate screen is %dx%d, want 1x1 until it is used", w, h)
	}

	// A resize on the main screen must not build it either.
	e.Resize(100, 30)
	if w, h := e.scrs[1].buf.Width(), e.scrs[1].buf.Height(); w != 1 || h != 1 {
		t.Fatalf("a resize built the alternate screen at %dx%d", w, h)
	}

	e.WriteString("\x1b[?1049h\x1b[5;10Halt-screen-text")
	if !e.IsAltScreen() {
		t.Fatal("not on the alternate screen after 1049h")
	}
	if w, h := e.Width(), e.Height(); w != 100 || h != 30 {
		t.Fatalf("alternate screen is %dx%d on first use, want the main screen's 100x30", w, h)
	}
	if got := altText(e, 4); !strings.Contains(got, "alt-screen-text") {
		t.Fatalf("row 5 of the alternate screen is %q, want the text written there", got)
	}

	// Once built it follows resizes like it always did.
	e.Resize(60, 20)
	if w, h := e.Width(), e.Height(); w != 60 || h != 20 {
		t.Fatalf("alternate screen is %dx%d after a resize, want 60x20", w, h)
	}
	e.WriteString("\x1b[?1049l")
	if e.IsAltScreen() {
		t.Fatal("still on the alternate screen after 1049l")
	}
}
