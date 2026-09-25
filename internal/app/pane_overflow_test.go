package app

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// A pane must draw inside its own rectangle and nowhere else.
//
// The way it stopped doing that: a snap animation, which every retile creates,
// interpolates a window's X, Y, Width and Height on every tick and deliberately
// leaves the emulator alone until the transition ends. So for the length of the
// animation the pane's rectangle is the new one while its body is still the old
// one, and renderTerminal hands back a body sized by the emulator: the
// unfocused fast path returns the emulator's own Render() verbatim.
//
// lipgloss's Width and Height pad but never truncate, so that oversized body
// grew the box: the bottom border landed one or more rows BELOW the pane's own
// bottom edge, on top of the pane beneath it or in the status bar. In a three
// pane layout, one tall pane on the left and two stacked on the right, the
// bottom edges stopped lining up and the left pane's frame ran into the status
// bar.
func TestWindowBoxNeverExceedsItsRectangle(t *testing.T) {
	// The bug is in the unfocused path, so this window must not be focused.
	win := newTestWindow(t, "overflow-0001", 60, 34)
	other := newTestWindow(t, "overflow-0002", 60, 34)
	m := newTestOS(other)
	m.Windows = append(m.Windows, win)

	win.LockIO()
	for y := 1; y <= 32; y++ {
		_, _ = win.Terminal.Write([]byte("\x1b[" + itoa(y) + ";1Hline " + itoa(y) + " of a pane that is about to be made smaller"))
	}
	win.UnlockIO()
	win.MarkContentDirty()

	// Shrink the rectangle the way an in-flight snap animation does: geometry
	// only, emulator untouched.
	win.Width, win.Height = 40, 20
	win.MarkPositionDirty()

	box := m.renderWindowBox(win, 1, false, theme.BorderUnfocused())
	w, h := lipgloss.Size(box)
	if h > win.Height {
		t.Errorf("box is %d rows for a %d row window: it overflows by %d", h, win.Height, h-win.Height)
	}
	if w > win.Width {
		t.Errorf("box is %d columns for a %d column window: it overflows by %d", w, win.Width, w-win.Width)
	}
	// The bottom border has to be the last row of the box, not a row past it,
	// and the body must not be what is left showing there. The border glyphs
	// come from config because other tests in this package change them.
	lines := strings.Split(box, "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, m.Settings.GetWindowBorderBottomLeft()) || strings.Contains(last, "line ") {
		t.Errorf("last row of the box is not the bottom border: %q", last)
	}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
