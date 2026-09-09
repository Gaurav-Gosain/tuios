package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// keystrokeOS builds a host of n bordered panes filled with ordinary shell-like
// text, the state a client is in between keystrokes.
func keystrokeOS(tb testing.TB, n, cols, rows int) *OS {
	tb.Helper()
	m := &OS{
		Settings:         config.Global,
		Windows:          make([]*terminal.Window, 0, n),
		FocusedWindow:    0,
		WorkspaceFocus:   map[int]int{},
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		Width:            cols,
		Height:           rows,
		Mode:             TerminalMode,
	}
	gridCols := 1
	for gridCols*gridCols < n {
		gridCols++
	}
	gridRows := (n + gridCols - 1) / gridCols
	winW, winH := cols/gridCols, rows/gridRows
	for i := range n {
		win := newTestWindow(tb, fmt.Sprintf("key-%d-%d", n, i), winW, winH)
		win.X, win.Y = (i%gridCols)*winW, (i/gridCols)*winH
		win.Width, win.Height = winW, winH
		win.Workspace = 1
		fillWindow(tb, win, win.ContentWidth(), win.ContentHeight())
		m.Windows = append(m.Windows, win)
	}
	return m
}

// BenchmarkKeystrokeFrame is the common frame: the user types one character
// into the focused pane and every other pane is idle. It is the whole client
// frame, compose and emit, so it is the latency between a keystroke's echo
// reaching the emulator and its bytes leaving for the terminal.
func BenchmarkKeystrokeFrame(b *testing.B) {
	for _, n := range []int{1, 2, 4, 9} {
		b.Run(fmt.Sprintf("panes-%d", n), func(b *testing.B) {
			m := keystrokeOS(b, n, realCols, realRows)
			sink := newFrameSink(realCols, realRows)
			for _, w := range m.Windows {
				w.MarkContentDirty()
			}
			sink.emit(m.composeFrame())
			focused := m.Windows[0]
			var out int
			i := 0
			b.ReportAllocs()
			for b.Loop() {
				focused.LockIO()
				_, _ = focused.Terminal.Write(fmt.Appendf(nil, "\x1b[2;3H%c", 'a'+byte(i%26)))
				focused.UnlockIO()
				focused.MarkContentDirty()
				out = sink.emit(m.composeFrame())
				i++
			}
			b.ReportMetric(float64(out), "bytes/frame")
		})
	}
}

// tiledKeystrokeOS is keystrokeOS under the shipped defaults: BSP tiling with
// shared borders, so the panes are borderless and the dividers between them
// are the separator overlay.
func tiledKeystrokeOS(tb testing.TB, n, cols, rows int) *OS {
	tb.Helper()
	m := &OS{
		Settings:             config.Global,
		SharedBorders:        true,
		PaneGap:              config.Global.PaneGap,
		Windows:              make([]*terminal.Window, 0, n),
		FocusedWindow:        0,
		WorkspaceFocus:       map[int]int{},
		WorkspaceTrees:       map[int]*layout.BSPTree{},
		NumWorkspaces:        9,
		CurrentWorkspace:     1,
		Width:                cols,
		Height:               rows,
		Mode:                 TerminalMode,
		AutoTiling:           true,
		UseBSPLayout:         true,
		MasterRatio:          0.5,
		WorkspaceLayouts:     map[int][]WindowLayout{},
		WorkspaceHasCustom:   map[int]bool{},
		WorkspaceMasterRatio: map[int]float64{},
		PendingResizes:       map[string][2]int{},
	}
	for i := range n {
		win := newTestWindow(tb, fmt.Sprintf("tkey-%d-%d", n, i), 40, 20)
		win.Workspace = 1
		m.Windows = append(m.Windows, win)
	}
	m.TileAllWindows()
	m.ApplyPendingResizes()
	for _, w := range m.Windows {
		fillWindow(tb, w, w.ContentWidth(), w.ContentHeight())
	}
	return m
}

// BenchmarkKeystrokeFrameTiled is BenchmarkKeystrokeFrame on the default
// layout, where the divider overlay is part of every frame.
func BenchmarkKeystrokeFrameTiled(b *testing.B) {
	orig := config.Global.AnimationsEnabled
	config.Global.AnimationsEnabled = false
	b.Cleanup(func() { config.Global.AnimationsEnabled = orig })
	for _, n := range []int{2, 4, 9} {
		b.Run(fmt.Sprintf("panes-%d", n), func(b *testing.B) {
			m := tiledKeystrokeOS(b, n, realCols, realRows)
			sink := newFrameSink(realCols, realRows)
			for _, w := range m.Windows {
				w.MarkContentDirty()
			}
			sink.emit(m.composeFrame())
			focused := m.Windows[0]
			var out int
			i := 0
			b.ReportAllocs()
			for b.Loop() {
				focused.LockIO()
				_, _ = focused.Terminal.Write(fmt.Appendf(nil, "\x1b[2;3H%c", 'a'+byte(i%26)))
				focused.UnlockIO()
				focused.MarkContentDirty()
				out = sink.emit(m.composeFrame())
				i++
			}
			b.ReportMetric(float64(out), "bytes/frame")
		})
	}
}
