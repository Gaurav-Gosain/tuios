package app

import (
	"strings"
	"testing"
)

func TestDimReachesACellThatNamedNoColourOfItsOwn(t *testing.T) {
	// A shell prompt is mostly cells carrying no colour at all. Gating the dim
	// on "does this cell carry anything of its own" left the setting doing
	// nothing to most of a pane even with a theme set, which reads as a broken
	// option rather than a subtle one.
	withDim(t, 60)
	win := newTestWindow(t, "plain", 40, 6)
	win.WriteOutput([]byte("plain text with no sgr at all\r\n"))
	win.MarkContentDirty()
	m := newTestOS(win)

	focused := m.renderTerminal(win, true, false)
	win.MarkContentDirty()
	unfocused := m.renderTerminal(win, false, false)

	if focused == unfocused {
		t.Fatal("an unfocused pane of uncoloured text rendered identically to the focused one")
	}
	if !strings.Contains(unfocused, "plain text with no sgr at all") {
		t.Error("the dim ate the content")
	}
}

func TestAScrolledBackPaneDoesNotServeAFrameFromTheOtherFocusState(t *testing.T) {
	// The scrollback path used to return the cache without the dim key, and it
	// is reached only once the keyed check has already failed, so it fired
	// exactly on the mismatch it was meant to catch.
	withDim(t, 50)
	win := dimTestWindow(t, 40, 6)
	win.ScrollbackOffset = 1
	m := newTestOS(win)

	unfocused := m.renderTerminal(win, false, false)
	focused := m.renderTerminal(win, true, false)
	if focused == unfocused {
		t.Error("a scrolled-back pane served its dimmed frame to a focused render")
	}
}
