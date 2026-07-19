package app

import (
	"fmt"
	"testing"
)

// BenchmarkIsBlankRender measures the guard added to the render cache in
// isolation. The realistic case is a pane with text in it, where the scan exits
// on the first visible byte; the blank pane is the worst case, where it walks
// the whole frame.
func BenchmarkIsBlankRender(b *testing.B) {
	var full, blank string
	for y := range 40 {
		full += fmt.Sprintf("\x1b[38;5;12mline %02d content goes here and fills the row\x1b[m\n", y)
		blank += "                                                            \n"
	}

	b.Run("typical-visible", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = isBlankRender(full)
		}
	})
	b.Run("worst-case-blank", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = isBlankRender(blank)
		}
	})
}

// BenchmarkRenderTerminalUnfocused measures the whole unfocused render path, so
// the guard above can be read as a fraction of the work one window already
// costs per frame.
func BenchmarkRenderTerminalUnfocused(b *testing.B) {
	win := newTestWindow(b, "bench-unfocused-01", 120, 40)
	m := newTestOS(win)

	win.LockIO()
	for y := 1; y <= 40; y++ {
		_, _ = win.Terminal.Write(fmt.Appendf(nil,
			"\x1b[%d;1H\x1b[38;5;12mline %02d content goes here and fills the row\x1b[m", y, y))
	}
	win.UnlockIO()

	b.ReportAllocs()
	for b.Loop() {
		// Mark dirty each iteration so the cache short circuit does not turn
		// this into a measurement of a map lookup.
		win.MarkContentDirty()
		_ = m.renderTerminal(win, false, false)
	}
}
