package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// benchOS builds an OS with n tiled windows filling a 207x55 viewport, each
// with painted content, which is the state a frame is composed from.
func benchOS(tb testing.TB, n int) *OS {
	tb.Helper()

	m := &OS{
		Windows:          make([]*terminal.Window, 0, n),
		FocusedWindow:    0,
		WorkspaceFocus:   map[int]int{},
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		Width:            realCols,
		Height:           realRows,
	}

	// Tile the windows in a grid so each one is a realistic fraction of the
	// screen rather than n copies of the whole thing.
	cols := 1
	for cols*cols < n {
		cols++
	}
	rows := (n + cols - 1) / cols
	winW := realCols / cols
	winH := realRows / rows

	for i := range n {
		win := benchWindow(tb, fmt.Sprintf("comp-%d-%d", n, i), winW, winH)
		win.X = (i % cols) * winW
		win.Y = (i / cols) * winH
		win.Width = winW
		win.Height = winH
		win.Workspace = 1
		win.Tiled = true
		m.Windows = append(m.Windows, win)
	}
	return m
}

// BenchmarkCompositorGetCanvas measures the whole compositor path: the
// per-window loop, the terminal render, the border box, the clip, and the
// layer compose. This is the frame, so it is the number that decides the
// ceiling on frame rate.
//
// The two variants separate the case that matters from the one that is easy.
// "all-dirty" is every window having produced output since the last frame,
// which is the worst case. "one-dirty" is the realistic case: the user is
// typing in one pane and the rest are idle, so every other window should take
// the cached-layer branch and cost almost nothing.
func BenchmarkCompositorGetCanvas(b *testing.B) {
	for _, n := range []int{1, 4, 9} {
		b.Run(fmt.Sprintf("windows-%d/all-dirty", n), func(b *testing.B) {
			m := benchOS(b, n)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				for _, w := range m.Windows {
					w.MarkContentDirty()
				}
				_ = m.GetCanvas(false)
			}
		})

		b.Run(fmt.Sprintf("windows-%d/one-dirty", n), func(b *testing.B) {
			m := benchOS(b, n)
			// Prime every layer so the idle windows have a cache to hold.
			for _, w := range m.Windows {
				w.MarkContentDirty()
			}
			_ = m.GetCanvas(false)

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				m.Windows[0].MarkContentDirty()
				_ = m.GetCanvas(false)
			}
		})
	}
}
