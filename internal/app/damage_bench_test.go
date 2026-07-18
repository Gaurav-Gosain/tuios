package app

import (
	"fmt"
	"testing"
)

// The review asked whether damage tracking actually limits work end to end at
// 207x55, and whether anything scales with total cells rather than changed
// cells. These two benchmarks answer it directly, by comparing the cost of a
// frame in which a single cell changed against one in which the whole screen
// did.
//
// The dirty signal is a per-window boolean (Window.ContentDirty), so any
// output at all, one byte or a full repaint, invalidates the whole pane and
// the next render walks every cell. The emulator does track damage per line
// (Emulator.Touched returns line data), but the render path does not consult
// it. If the two benchmarks below report the same number, damage tracking
// limits work only at window granularity, not at line or cell granularity.
//
// This is a measurement, not an assertion: it is here so the next person
// optimizing the render loop starts from the number rather than from a guess.

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
			// Change exactly one cell, the way a shell echoing a keystroke does.
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
