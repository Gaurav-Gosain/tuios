package app

// A client with a screen (screen.go) diffs the composed canvas. A client
// without one hands Bubble Tea the frame string, which its renderer parses
// back into cells and diffs. These hold the two to the same terminal screen
// and measure what the string costs.
//
// cellSink diffs the canvas with uv.TerminalRenderer directly; frameSink is
// the string path. Both are configured the same way and mark every row
// touched, so the diff does the same work.

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/termio"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"
)

// cellSink is frameSink without the string: the canvas is the new buffer.
type cellSink struct {
	buf     bytes.Buffer
	scr     *uv.TerminalRenderer
	rb      uv.RenderBuffer
	touched []uv.LineData
}

func newCellSink() *cellSink {
	s := &cellSink{}
	s.scr = uv.NewTerminalRenderer(&s.buf, nil)
	s.scr.SetColorProfile(colorprofile.TrueColor)
	s.scr.SetRelativeCursor(false)
	s.scr.SetFullscreen(true)
	s.scr.SetTabStops(-1)
	s.scr.SetScrollOptim(true)
	return s
}

// emit diffs c against the last frame and returns the bytes it produced.
func (s *cellSink) emit(c *frameCanvas) int {
	s.buf.Reset()
	w, h := c.Width(), c.Height()
	s.rb.Buffer = &c.Buffer
	if len(s.rb.Touched) != h {
		s.rb.Touched = make([]*uv.LineData, h)
		s.touched = make([]uv.LineData, h)
	}
	for i := range s.rb.Touched {
		s.touched[i] = uv.LineData{FirstCell: 0, LastCell: w}
		s.rb.Touched[i] = &s.touched[i]
	}
	s.scr.Render(&s.rb)
	_ = s.scr.Flush()
	return s.buf.Len()
}

// composeCells composes a frame and returns its canvas. The cases here never
// take the saver or the fullscreen fast path.
func composeCells(tb testing.TB, m *OS) *frameCanvas {
	c, _ := m.composeFrameParts()
	if c == nil {
		tb.Fatal("frame was not composed on the canvas")
	}
	return c
}

func BenchmarkKeystrokeFrameCells(b *testing.B) {
	orig := config.Global.Motion
	config.Global.Motion = config.MotionNone
	b.Cleanup(func() { config.Global.Motion = orig })
	type build func(testing.TB, int, int, int) *OS
	for _, layout := range []struct {
		name  string
		build build
	}{{"floating", keystrokeOS}, {"tiled", tiledKeystrokeOS}} {
		for _, n := range []int{1, 4, 9} {
			for _, path := range []string{"string", "cells"} {
				b.Run(fmt.Sprintf("%s/panes-%d/%s", layout.name, n, path), func(b *testing.B) {
					m := layout.build(b, n, realCols, realRows)
					for _, w := range m.Windows {
						w.MarkContentDirty()
					}
					str := newFrameSink(realCols, realRows)
					cells := newCellSink()
					frame := func() int {
						if path == "cells" {
							return cells.emit(composeCells(b, m))
						}
						return str.emit(string(m.composeView()))
					}
					frame()
					focused := m.Windows[0]
					var out int
					i := 0
					b.ReportAllocs()
					for b.Loop() {
						focused.LockIO()
						_, _ = focused.Terminal.Write(fmt.Appendf(nil, "\x1b[2;3H%c", 'a'+byte(i%26)))
						focused.UnlockIO()
						focused.MarkContentDirty()
						out = frame()
						i++
					}
					b.ReportMetric(float64(out), "bytes/frame")
				})
			}
		}
	}
}

func BenchmarkClientFrameCells(b *testing.B) {
	for _, n := range []int{1, 9} {
		for _, path := range []string{"string", "cells"} {
			b.Run(fmt.Sprintf("panes-%d/%s", n, path), func(b *testing.B) {
				m := floodOS(b, n, realCols, realRows)
				rng := rand.New(rand.NewSource(2))
				str := newFrameSink(realCols, realRows)
				cells := newCellSink()
				var out int
				b.ReportAllocs()
				for b.Loop() {
					for _, w := range m.Windows {
						floodFrame(b, w, rng, w.ContentWidth(), w.ContentHeight())
						w.InvalidateCache()
						w.MarkContentDirty()
					}
					if path == "cells" {
						out = cells.emit(composeCells(b, m))
					} else {
						out = str.emit(string(m.composeView()))
					}
				}
				b.ReportMetric(float64(out), "bytes/frame")
			})
		}
	}
}

// TestCellFrameDrawsTheSameScreen feeds the bytes of both sinks, frame by
// frame, into an emulator each and requires the same cells on every frame.
// Both sinks diff the same canvas: the string sink takes canvas.Render(), the
// one bubbletea gets today, and the cell sink takes the canvas itself.
func TestCellFrameDrawsTheSameScreen(t *testing.T) {
	orig := config.Global.Motion
	config.Global.Motion = config.MotionNone
	t.Cleanup(func() { config.Global.Motion = orig })
	cases := []struct {
		name  string
		os    func(testing.TB) *OS
		frame func(m *OS, i int)
	}{
		{"keystroke-floating-9", func(tb testing.TB) *OS { return keystrokeOS(tb, 9, realCols, realRows) }, typeOne},
		{"keystroke-tiled-9", func(tb testing.TB) *OS { return tiledKeystrokeOS(tb, 9, realCols, realRows) }, typeOne},
		{"flood-9", func(tb testing.TB) *OS { return floodOS(tb, 9, realCols, realRows) }, nil},
		{"palette-over-tiled-4", func(tb testing.TB) *OS {
			m := tiledKeystrokeOS(tb, 4, realCols, realRows)
			m.ShowCommandPalette = true
			return m
		}, typeOne},
		{"dock-top-tiled-2", func(tb testing.TB) *OS {
			m := tiledKeystrokeOS(tb, 2, realCols, realRows)
			m.Settings.DockbarPosition = "top"
			return m
		}, typeOne},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.os(t)
			rng := rand.New(rand.NewSource(3))
			str, cells := newFrameSink(realCols, realRows), newCellSink()
			strScreen, cellScreen := vt.NewEmulator(realCols, realRows), vt.NewEmulator(realCols, realRows)
			for i := range 40 {
				if tc.frame != nil {
					tc.frame(m, i)
				} else {
					for _, w := range m.Windows {
						floodFrame(t, w, rng, w.ContentWidth(), w.ContentHeight())
						w.InvalidateCache()
						w.MarkContentDirty()
					}
				}
				for _, w := range m.Windows {
					if i == 0 {
						w.MarkContentDirty()
					}
				}
				c := composeCells(t, m)
				str.emit(c.Render())
				cells.emit(c)
				_, _ = strScreen.Write(str.buf.Bytes())
				_, _ = cellScreen.Write(cells.buf.Bytes())
				if d := firstCellDiff(strScreen, cellScreen); d != "" {
					t.Fatalf("frame %d: %s", i, d)
				}
			}
		})
	}
}

func typeOne(m *OS, i int) {
	w := m.Windows[0]
	w.LockIO()
	_, _ = w.Terminal.Write(fmt.Appendf(nil, "\x1b[2;3H%c", 'a'+byte(i%26)))
	w.UnlockIO()
	w.MarkContentDirty()
}

func firstCellDiff(a, b *vt.Emulator) string {
	for y := range a.Height() {
		for x := range a.Width() {
			ca, cb := a.CellAt(x, y), b.CellAt(x, y)
			switch {
			case ca == nil && cb == nil:
				continue
			case ca == nil || cb == nil:
				return fmt.Sprintf("cell %d,%d: one side nil (%v, %v)", x, y, ca, cb)
			case ca.Content != cb.Content || ca.Width != cb.Width || !ca.Style.Equal(&cb.Style) || ca.Link != cb.Link:
				return fmt.Sprintf("cell %d,%d: string %q %+v, cells %q %+v", x, y, ca.Content, ca.Style, cb.Content, cb.Style)
			}
		}
	}
	return ""
}

// TestDirectPaneMatchesItsText is a wire compatibility check: a lone pane
// drawn from its emulator's cells (pane_cells.go) must leave the canvas with
// exactly the cells the pane's text, from renderTerminal, parses to. The
// content carries colours, attributes, a curly underline, wide characters and
// erased runs, with the dock above and below, and beside the rail.
func TestDirectPaneMatchesItsText(t *testing.T) {
	for _, tc := range []struct {
		dock        string
		rail, tiled bool
	}{{"bottom", false, false}, {"top", false, false}, {"bottom", true, false}, {"bottom", true, true}} {
		name := tc.dock
		if tc.rail {
			name += "-rail"
		}
		if tc.tiled {
			name += "-tiled"
		}
		t.Run(name, func(t *testing.T) {
			m, win := fastPathOS(t, tc.dock)
			if tc.tiled {
				// The shipped layout: a lone borderless tiled pane.
				m = keyPaneOS(t, io.Discard)
				win = m.Windows[0]
				win.WriteOutput([]byte("\x1b[2J\x1b[H" + fastPathContent))
				win.MarkContentDirty()
			} else if tc.rail {
				m.Settings.SidebarEnabled = true
				m.Settings.SidebarPosition = "left"
				m.Settings.SidebarWidth = config.SidebarDefaultWidth
				m.SessionName = "work"
				win.X, win.Width = m.GetLeftMargin(), m.GetContentWidth()
				win.Resize(win.Width, win.Height)
				win.WriteOutput([]byte("\x1b[2J\x1b[H" + fastPathContent))
				win.MarkPositionDirty()
				if m.GetSidebarWidth() == 0 {
					t.Fatal("the rail is not shown")
				}
			}
			if m.screen == nil {
				m.UseScreen(io.Discard, []string{"TERM=xterm-256color"}, colorprofile.TrueColor, termio.Output{Raw: true})
			}
			d := m.chooseDirectPane()
			if d.w == nil {
				t.Fatal("the lone pane was not drawn from its cells")
			}
			d.release()

			win.MarkContentDirty()
			c, _ := m.composeFrameParts()
			direct := uv.NewBuffer(c.Width(), c.Height())
			for y := range c.Lines {
				copy(direct.Lines[y], c.Lines[y])
			}

			prev := fastPathDisabled
			fastPathDisabled = true
			t.Cleanup(func() { fastPathDisabled = prev })
			win.MarkContentDirty()
			ref, _ := m.composeFrameParts()
			for y := range ref.Lines {
				for x := range ref.Lines[y] {
					a, b := &direct.Lines[y][x], &ref.Lines[y][x]
					if !a.Equal(b) {
						t.Fatalf("cell %d,%d: from the emulator %q %+v, from the text %q %+v", x, y, a.Content, a.Style, b.Content, b.Style)
					}
				}
			}
		})
	}
}

// BenchmarkLonePaneKeystroke is the frame a key typed into a lone fullscreen
// pane composes, on the cell path (a screen: the pane drawn from its
// emulator, pane_cells.go) and on the string path, compose and the write's
// diff together.
func BenchmarkLonePaneKeystroke(b *testing.B) {
	orig := config.Global.Motion
	config.Global.Motion = config.MotionNone
	b.Cleanup(func() { config.Global.Motion = orig })
	for _, path := range []string{"string", "cells"} {
		b.Run(path, func(b *testing.B) {
			m, win := fastPathOS(b, "bottom")
			win.Resize(realCols, realRows-2)
			m.Width, m.Height = realCols, realRows
			win.X, win.Y, win.Width, win.Height = 0, m.GetTopMargin(), m.GetRenderWidth(), m.GetUsableHeight()
			win.Resize(win.Width, win.Height)
			fillWindow(b, win, win.ContentWidth(), win.ContentHeight())
			str := newFrameSink(m.GetRenderWidth(), m.GetRenderHeight())
			if path == "cells" {
				m.UseScreen(io.Discard, []string{"TERM=xterm-256color"}, colorprofile.TrueColor, termio.Output{Raw: true})
			}
			frame := func() {
				if path == "cells" {
					_ = string(m.composeView())
					m.screen.Commit()
					return
				}
				str.emit(string(m.composeView()))
			}
			win.MarkContentDirty()
			frame()
			i := 0
			b.ReportAllocs()
			for b.Loop() {
				win.LockIO()
				_, _ = win.Terminal.Write(fmt.Appendf(nil, "\x1b[2;3H%c", 'a'+byte(i%26)))
				win.UnlockIO()
				win.MarkContentDirty()
				frame()
				i++
			}
		})
	}
}
