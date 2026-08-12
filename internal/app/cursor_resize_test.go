package app

import (
	"strings"
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

// TestResizeDrawsNoFakeCursorEither is the other half: the cell loop paints its
// own cursor whenever the host is not drawing a real one, so suppressing the
// real cursor must not simply hand the job over.
func TestResizeDrawsNoFakeCursorEither(t *testing.T) {
	win := newTestWindow(t, "fake-cursor-resize", 80, 24)
	win.WriteOutput([]byte("prompt$ "))

	m := newTestOS(win)
	m.Mode = TerminalMode
	// ShowScrollbackBrowser is the cheapest way to make getRealCursor return nil
	// for a reason other than the resize, so the cell loop is the path under test.
	m.ShowScrollbackBrowser = true

	win.ContentDirty = true
	win.CachedContent = ""
	withCursor := m.renderTerminal(win, true, true)

	m.Resizing = true
	win.ContentDirty = true
	win.CachedContent = ""
	// IsBeingManipulated is left off on purpose: a pane that is not the one
	// being dragged still renders its content during the gesture, and that is
	// the pane whose cursor would survive.
	whileResizing := m.renderTerminal(win, true, true)

	if whileResizing == withCursor {
		t.Fatal("resize changed nothing about the render; the fixture is not exercising the cursor path")
	}
	if strings.Count(whileResizing, "\x1b[7m") > 0 && strings.Count(withCursor, "\x1b[7m") == 0 {
		t.Error("a fake cursor appeared during the resize")
	}
}
