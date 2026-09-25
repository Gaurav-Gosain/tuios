package app

import (
	"testing"
)

// TestResizeDrawsNoCursor pins the guest cursor off for the length of a resize
// gesture. The pane under the pointer is showing the size readout rather than
// the guest's screen, so a cursor parked in it points at nothing and trails the
// layout as the panes move.
//
// The assertion is deliberately made with the mode left in TerminalMode. A
// resize borrows window management, which hides the cursor on its own, and a
// test that leaned on that would pass without testing anything.
func TestResizeDrawsNoCursor(t *testing.T) {
	win := newTestWindow(t, "cursor-resize", 80, 24)
	win.WriteOutput([]byte("prompt$ "))

	m := newTestOS(win)
	m.Mode = TerminalMode

	if m.getRealCursor() == nil {
		t.Fatal("no cursor before the gesture; the fixture proves nothing")
	}

	m.Resizing = true
	if c := m.getRealCursor(); c != nil {
		t.Errorf("cursor drawn at %v during a resize", *c)
	}

	m.Resizing = false
	if m.getRealCursor() == nil {
		t.Error("cursor not restored after the resize")
	}
}
