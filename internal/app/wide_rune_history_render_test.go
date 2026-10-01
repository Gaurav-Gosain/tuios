package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A scrolled-back history row whose wide rune sits across the pane's last
// column, after the pane narrowed.
//
// History keeps the width it was written at, so the stored line still holds
// the whole rune. The pane renderer has to clip the row to the pane with
// vt.ClipHistoryRow. Without the clip the row comes out one column wider than
// the pane, and the frame's width cut later drops the whole wide cell: the
// last column loses the line's background, and a copy cursor or a selection
// on it. The end-to-end frame cannot see that loss in every layout (see
// e2e/tui/NEGATIVE_CONTROLS.md), so the renderer's own output is checked here.
//
// NEGATIVE CONTROL: drop the vt.ClipHistoryRow call in renderTerminal and the
// row is one column wider than the pane.
func TestHistoryRowWithAWideRuneAtTheEdgeIsClippedToThePane(t *testing.T) {
	win := newTestWindow(t, "wide-hist-0001", 40, 12)
	m := newTestOS(win)

	// Runes on odd columns ("A" then "世" pairs) and on even columns ("AB" then
	// pairs), on a blue background, so one line straddles any narrower edge.
	win.LockIO()
	_, _ = win.Terminal.Write([]byte("\x1b[44mA" + strings.Repeat("世", 18) + "\x1b[0m\r\n"))
	_, _ = win.Terminal.Write([]byte("\x1b[44mAB" + strings.Repeat("世", 18) + "\x1b[0m\r\n"))
	for i := range 30 {
		_, _ = win.Terminal.Write([]byte("filler " + itoa(i) + "\r\n"))
	}
	win.UnlockIO()
	win.MarkContentDirty()

	// Narrow the pane so its content is far shorter than the lines.
	win.Resize(14, 12)
	cols := win.ContentWidth()

	// Scroll back to the top, where the two lines are.
	scrollBack(t, win, win.ScrollbackLenSync())

	out := m.renderTerminal(win, true, false)
	rows := 0
	for row := range strings.SplitSeq(out, "\n") {
		plain := ansi.Strip(row)
		if !strings.HasPrefix(plain, "A") {
			continue
		}
		rows++
		// The positive half: the row reaches the edge, so the edge is in question.
		if !strings.Contains(plain, "世") {
			t.Fatalf("the history row %q holds no wide rune, so it tests nothing", plain)
		}
		if got := ansi.StringWidth(row); got != cols {
			t.Errorf("a history row is %d columns wide in a %d-column pane: %q", got, cols, plain)
		}
	}
	if rows != 2 {
		t.Fatalf("found %d of the 2 history rows in the frame:\n%s", rows, ansi.Strip(out))
	}
}
