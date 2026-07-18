package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/charmbracelet/x/ansi"
)

// The maintainer runs a 207x55 host terminal. Every benchmark here uses that as
// the realistic size rather than the 120x40 used by the older benchmarks, since
// the per-frame cost of the render path scales with total cells and the
// difference between the two is a factor of about 2.4.
const (
	realCols = 207
	realRows = 55
)

// fillWindow paints every row of a window's emulator with styled text, which is
// the shape of a frame the batching loop in renderTerminal actually has to
// work through: a colour run per line rather than a uniform blank screen.
func fillWindow(tb testing.TB, win *terminal.Window, cols, rows int) {
	tb.Helper()
	win.LockIO()
	defer win.UnlockIO()
	for y := 1; y <= rows; y++ {
		line := fmt.Sprintf("line %03d ", y)
		for len(line) < cols-12 {
			line += "content "
		}
		_, _ = win.Terminal.Write(fmt.Appendf(nil,
			"\x1b[%d;1H\x1b[38;5;%dm%s\x1b[m", y, 16+(y%200), line))
	}
}

// benchWindow builds a window at the given size with realistic painted content.
func benchWindow(tb testing.TB, id string, cols, rows int) *terminal.Window {
	tb.Helper()
	win := newTestWindow(tb, id, cols, rows)
	fillWindow(tb, win, cols, rows)
	return win
}

// BenchmarkRenderTerminalReal measures the two renderTerminal paths at the real
// host size. "unfocused" is the emulator's built-in Render, "focused" is the
// cell-by-cell loop with cursor overlay, which is the path the window the user
// is actually typing into takes on every frame.
func BenchmarkRenderTerminalReal(b *testing.B) {
	sizes := []struct {
		name       string
		cols, rows int
	}{
		{"120x40", 120, 40},
		{"207x55", realCols, realRows},
	}

	for _, sz := range sizes {
		b.Run(sz.name+"/unfocused", func(b *testing.B) {
			win := benchWindow(b, "bench-u-"+sz.name, sz.cols, sz.rows)
			m := newTestOS(win)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				win.MarkContentDirty()
				_ = m.renderTerminal(win, false, false)
			}
		})

		b.Run(sz.name+"/focused", func(b *testing.B) {
			win := benchWindow(b, "bench-f-"+sz.name, sz.cols, sz.rows)
			m := newTestOS(win)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				win.MarkContentDirty()
				_ = m.renderTerminal(win, true, true)
			}
		})

		// The clean-cache hit is what the great majority of windows take on a
		// typical frame, so its cost bounds the floor of a multi-window frame.
		b.Run(sz.name+"/cached", func(b *testing.B) {
			win := benchWindow(b, "bench-c-"+sz.name, sz.cols, sz.rows)
			m := newTestOS(win)
			win.MarkContentDirty()
			_ = m.renderTerminal(win, false, false)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = m.renderTerminal(win, false, false)
			}
		})
	}
}

// renderedFrame returns a realistic rendered window frame for the clip
// benchmarks: styled, full width, with the trailing-space trimming the
// unfocused fast path performs.
func renderedFrame(tb testing.TB, cols, rows int) string {
	tb.Helper()
	win := benchWindow(tb, "bench-frame", cols, rows)
	m := newTestOS(win)
	win.MarkContentDirty()
	return m.renderTerminal(win, false, false)
}

// BenchmarkClipWindowContent measures the compositor's per-window clip. This is
// called once per redrawn window per frame, and it walks every line of the
// frame twice in the clipping cases: once for the width scan and once for the
// clip itself.
func BenchmarkClipWindowContent(b *testing.B) {
	frame := renderedFrame(b, realCols, realRows)

	b.Run("fully-visible", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _, _ = clipWindowContent(frame, 0, 0, realCols, realRows)
		}
	})

	// A window pushed partly off the left edge takes the expensive rune-by-rune
	// escape-preserving clip path.
	b.Run("clipped-left", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _, _ = clipWindowContent(frame, -20, 0, realCols, realRows)
		}
	})

	b.Run("clipped-right", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _, _ = clipWindowContent(frame, 40, 0, realCols, realRows)
		}
	})

	b.Run("offscreen", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _, _ = clipWindowContent(frame, -1000, 0, realCols, realRows)
		}
	})
}

// BenchmarkIsBlankRenderReal measures the blank-frame guard at the real size,
// on the frame shape it actually sees. cacheRender runs it on every rendered
// frame, so it is on the hot path once per window per frame.
func BenchmarkIsBlankRenderReal(b *testing.B) {
	frame := renderedFrame(b, realCols, realRows)
	blank := strings.Repeat(strings.Repeat(" ", realCols)+"\n", realRows)

	b.Run("typical", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = isBlankRender(frame)
		}
	})
	b.Run("blank", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = isBlankRender(blank)
		}
	})
}

// BenchmarkFrameWidthImplementations compares the width measurement against the
// ansi.StringWidth loop it replaced.
//
// It exists because the machine these numbers are taken on is shared, and a
// wall-time figure quoted from one run and compared against a figure from
// another is worthless when load average swings between 5 and 60: the same
// benchmark measured 278us and 2.5ms an hour apart with no code change. Both
// variants here run in one process, interleaved by -count, so they see the same
// load and the same cache state, and the ratio between them is meaningful even
// when neither absolute number is.
func BenchmarkFrameWidthImplementations(b *testing.B) {
	lines := strings.Split(renderedFrame(b, realCols, realRows), "\n")

	b.Run("reference-stringwidth", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			widest := 0
			for _, line := range lines {
				if w := ansi.StringWidth(line); w > widest {
					widest = w
				}
			}
			_ = widest
		}
	})

	b.Run("fast-path", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = framesWidth(lines)
		}
	})
}
