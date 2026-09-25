package app

import (
	"fmt"
	"testing"
)

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
