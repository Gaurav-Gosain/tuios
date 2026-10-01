package vt

// Resize cost on a pane whose scrollback ring is full. Built through
// NewWithScrollback, so the same benchmark measures whichever backend the
// build selected:
//
//	go test ./internal/vt/ -run XXX -bench BenchmarkResizeFullRing
//	go test -tags ghostty ./internal/vt/ -run XXX -bench BenchmarkResizeFullRing
//
// The ring holds 10,000 rows of a log whose lines are longer than the pane, so
// every line wraps and a reflowing backend has every row to rewrap. Each
// iteration is one step of a window drag: the width or height moves by one
// column or row and back.

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

const (
	benchRingCols = 207
	benchRingRows = 55
	benchRingCap  = DefaultScrollbackSize
)

func fullRingTerminal(tb testing.TB, long bool) Terminal {
	tb.Helper()
	tm := NewWithScrollback(benchRingCols, benchRingRows, benchRingCap)
	var b strings.Builder
	if long {
		// 300 columns of text at 207 wide: two rows a line, so 5,000 lines
		// fill the ring and a little more scrolls it.
		for i := range benchRingCap/2 + benchRingRows {
			fmt.Fprintf(&b, "\x1b[3%dm%06d\x1b[m %s\r\n", i%7+1, i, strings.Repeat("lorem ipsum dolor sit amet ", 11)[:293])
		}
	} else {
		// A build log: lines that fit, so nothing on the screen wraps.
		for i := range benchRingCap + benchRingRows {
			fmt.Fprintf(&b, "\x1b[3%dm%06d\x1b[m compiling package %d\r\n", i%7+1, i, i)
		}
	}
	b.WriteString("$ ")
	_, _ = tm.Write([]byte(b.String()))
	return tm
}

func benchResize(b *testing.B, step func(i int) (int, int), frame bool) {
	benchResizeOn(b, true, step, frame)
}

func benchResizeOn(b *testing.B, long bool, step func(i int) (int, int), frame bool) {
	tm := fullRingTerminal(b, long)
	defer func() { _ = tm.Close() }()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	heap, rows := float64(ms.HeapInuse)/(1<<20), float64(tm.ScrollbackLen())
	b.ReportAllocs()
	b.ResetTimer()
	defer func() {
		b.ReportMetric(heap, "heapMB")
		b.ReportMetric(rows, "historyRows")
	}()
	for i := range b.N {
		w, h := step(i)
		tm.Resize(w, h)
		if frame {
			// What the next frame reads: every cell of the screen.
			for y := range h {
				for x := range w {
					_ = tm.CellAt(x, y)
				}
			}
		}
	}
}

func BenchmarkResizeFullRingWidth(b *testing.B) {
	benchResize(b, func(i int) (int, int) { return benchRingCols - i%2, benchRingRows }, false)
}

func BenchmarkResizeFullRingHeight(b *testing.B) {
	benchResize(b, func(i int) (int, int) { return benchRingCols, benchRingRows - i%2 }, false)
}

func BenchmarkResizeFullRingWidthFrame(b *testing.B) {
	benchResize(b, func(i int) (int, int) { return benchRingCols - i%2, benchRingRows }, true)
}

// BenchmarkResizeFullRingDrag is a drag from 207 columns down to 120 and
// back, one column a step: what a pane edge dragged across half the screen
// costs if every step reaches the emulator.
func BenchmarkResizeFullRingDrag(b *testing.B) {
	benchResize(b, func(i int) (int, int) {
		k := i % 174
		if k < 87 {
			return benchRingCols - k, benchRingRows
		}
		return benchRingCols - 174 + k, benchRingRows
	}, false)
}

// BenchmarkResizeFullRingShortLines is the drag step on a pane whose lines
// all fit: the reflow is skipped (fitsWithoutReflow).
func BenchmarkResizeFullRingShortLines(b *testing.B) {
	benchResizeOn(b, false, func(i int) (int, int) { return benchRingCols - i%2, benchRingRows }, false)
}
