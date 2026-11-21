package app

import (
	"fmt"
	"testing"
)

// BenchmarkDamageOneCellVsFullScreen compares the per-frame render cost when a
// single cell changed against the cost when the entire screen changed.
func BenchmarkDamageOneCellVsFullScreen(b *testing.B) {
	b.Run("one-cell-changed", func(b *testing.B) {
		win := benchWindow(b, "damage-one", realCols, realRows)
		m := newTestOS(win)
		b.ReportAllocs()
		b.ResetTimer()
		i := 0
		for b.Loop() {
			win.LockIO()
			_, _ = win.Terminal.Write(fmt.Appendf(nil, "\x1b[1;1H%c", 'a'+byte(i%26)))
			win.UnlockIO()
			win.MarkContentDirty()
			_ = m.renderTerminal(win, true, true)
			i++
		}
	})

	b.Run("full-screen-changed", func(b *testing.B) {
		win := benchWindow(b, "damage-full", realCols, realRows)
		m := newTestOS(win)
		b.ReportAllocs()
		b.ResetTimer()
		i := 0
		for b.Loop() {
			win.LockIO()
			for y := 1; y <= realRows; y++ {
				_, _ = win.Terminal.Write(fmt.Appendf(nil, "\x1b[%d;1H%c", y, 'a'+byte((i+y)%26)))
			}
			win.UnlockIO()
			win.MarkContentDirty()
			_ = m.renderTerminal(win, true, true)
			i++
		}
	})
}

// BenchmarkRenderScalingWithCells shows how the render cost grows with the
// total cell count, independent of how much of the screen actually changed.
// Linear growth here is the same finding stated a second way: the unit of work
// is the pane, not the change.
func BenchmarkRenderScalingWithCells(b *testing.B) {
	sizes := []struct {
		name       string
		cols, rows int
	}{
		{"80x24", 80, 24},
		{"120x40", 120, 40},
		{"207x55", realCols, realRows},
		{"280x70", 280, 70},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			win := benchWindow(b, "scale-"+sz.name, sz.cols, sz.rows)
			m := newTestOS(win)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				win.MarkContentDirty()
				_ = m.renderTerminal(win, true, true)
			}
			b.ReportMetric(float64(sz.cols*sz.rows), "cells")
		})
	}
}
