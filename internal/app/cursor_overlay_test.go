package app

import (
	"regexp"
	"testing"
)

// TestOverlayDrawsNoCursor pins the pane's cursor off while a panel is open.
// The panel covers the pane, so a cursor parked underneath points at nothing,
// and the panels that take typing draw their own caret as a character, so the
// terminal cursor would be a second mark next to it.
//
// The mode is deliberately left in TerminalMode. Window management hides the
// cursor on its own, and a test that leaned on that would pass without testing
// anything.
func TestOverlayDrawsNoCursor(t *testing.T) {
	win := newTestWindow(t, "cursor-overlay", 80, 24)
	win.WriteOutput([]byte("prompt$ "))

	m := newTestOS(win)
	m.Mode = TerminalMode

	if m.getRealCursor() == nil {
		t.Fatal("no cursor before the panel opens; the fixture proves nothing")
	}

	m.ShowCommandPalette = true
	if c := m.getRealCursor(); c != nil {
		t.Errorf("cursor drawn at %v while the command palette is open", *c)
	}

	m.ShowCommandPalette = false
	if m.getRealCursor() == nil {
		t.Error("cursor not restored after the panel closed")
	}
}

// TestOverlayDrawsNoFakeCursorEither is the other half. The cell loop paints its
// own cursor whenever the host is not drawing a real one, so hiding the real
// cursor must not hand the job over: that would move the mark rather than
// remove it.
func TestOverlayDrawsNoFakeCursorEither(t *testing.T) {
	win := newTestWindow(t, "fake-cursor-overlay", 80, 24)
	win.WriteOutput([]byte("prompt$ "))

	m := newTestOS(win)
	m.Mode = TerminalMode
	// ShowScrollbackBrowser makes getRealCursor return nil without gating the
	// cell loop, so the fake cursor is drawn and the baseline has something to
	// lose. A baseline that already drew no cursor would pass whatever this
	// code did.
	m.ShowScrollbackBrowser = true

	win.ContentDirty = true
	win.CachedContent = ""
	baseline := m.renderTerminal(win, true, true)
	if countFakeCursors(baseline) == 0 {
		t.Fatal("the baseline draws no fake cursor; the fixture is not exercising the cursor path")
	}

	m.ShowCommandPalette = true
	win.ContentDirty = true
	win.CachedContent = ""
	underPanel := m.renderTerminal(win, true, true)

	if n := countFakeCursors(underPanel); n > 0 {
		t.Errorf("%d fake cursors still drawn in the pane under the panel", n)
	}
}

// countFakeCursors counts the cells the cell loop painted as a cursor.
//
// It looks for a foreground and a background set in one sequence, which is what
// the cursor cell emits and what an unstyled prompt does not. The obvious probe
// is reverse video, and the resize test next door used it, but the cursor cell
// has never been drawn that way: it swaps the two colours explicitly, so
// counting ESC[7m could not fail and never did.
func countFakeCursors(render string) int {
	return len(regexp.MustCompile(`\x1b\[38;2;[0-9;]*;48;2;[0-9;]*m`).FindAllString(render, -1))
}
