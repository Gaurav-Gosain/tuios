package app

import (
	"fmt"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// TestRenderTerminalKeepsEveryStyleRun reads the focused pane's frame back
// into cells and checks each one against the emulator's cell. The cell loop
// batches neighbouring cells that share a style into one escape; a batch that
// swallowed a colour change would hand the second cell the first cell's
// colour, and nothing short of parsing the frame again would notice.
func TestRenderTerminalKeepsEveryStyleRun(t *testing.T) {
	win := newTestWindow(t, "runs", 26, 6)
	m := newTestOS(win)
	cols, rows := win.Terminal.Width(), win.Terminal.Height()
	win.LockIO()
	// The cursor is hidden so no cell wears the fake cursor's colours.
	_, _ = win.Terminal.Write([]byte("\x1b[?25l"))
	for y := 1; y <= rows; y++ {
		var b []byte
		b = fmt.Appendf(b, "\x1b[%d;1H", y)
		for x := range cols {
			// A different foreground on every cell, a background every third,
			// bold every fifth: every neighbour differs in something.
			b = fmt.Appendf(b, "\x1b[0;38;5;%dm", 16+(x*7+y)%200)
			if x%3 == 0 {
				b = fmt.Appendf(b, "\x1b[48;5;%dm", 100+x)
			}
			if x%5 == 0 {
				b = append(b, "\x1b[1m"...)
			}
			b = append(b, byte('a'+x%26))
		}
		_, _ = win.Terminal.Write(b)
	}
	win.UnlockIO()
	win.MarkContentDirty()

	for _, focused := range []bool{true, false} {
		win.MarkContentDirty()
		win.InvalidateCache()
		frame := m.renderTerminal(win, focused, true)
		canvas := &frameCanvas{}
		canvas.Resize(cols, rows)
		canvas.Clear()
		uv.NewStyledString(frame).Draw(canvas, canvas.Bounds())
		for y := range rows {
			for x := range cols {
				want := win.Terminal.CellAt(x, y)
				got := canvas.CellAt(x, y)
				if got.Content != want.Content || !got.Style.Equal(&want.Style) {
					t.Fatalf("focused=%v: cell (%d,%d) rendered as %q %+v, emulator holds %q %+v",
						focused, x, y, got.Content, got.Style, want.Content, want.Style)
				}
			}
		}
	}
}
