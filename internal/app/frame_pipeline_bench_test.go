package app

// The whole client frame, including the half of it nothing in this repo could
// measure.
//
// Every benchmark in tuios stops at the composed string. That leaves the step
// that turns the string into bytes on the wire unmeasured, and the last full
// attribution put a quarter of the client's time there, so a quarter of the
// cost was being optimised blind.
//
// The gap was never that the step is untestable. bubbletea's renderer is
// unexported (cursedRenderer, cursed_renderer.go:18) and only reachable through
// tea.Program, but the renderer does not do the work: it delegates to
// ultraviolet's TerminalRenderer, which is exported, takes a plain io.Writer,
// and needs no terminal at all. frameSink below is that delegation, reproduced
// from cursedRenderer.flush (cursed_renderer.go:257) with the sequence it
// performs on an alt-screen frame, which is the only kind tuios draws
// (render.go sets view.AltScreen unconditionally).
//
// What frameSink deliberately leaves out is the wrapper around the diff:
// alt-screen entry, cursor visibility, bracketed paste, synchronized output.
// Those are a fixed handful of bytes per frame and are unexported anyway. What
// it keeps is everything proportional to the frame: parsing the string into
// cells, diffing against the previous frame, and emitting the escape sequences
// for what changed.

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
)

// frameSink is the diff-and-emit half of a client frame: string in, escape
// sequences out, with the previous frame held so the diff has something to
// diff against.
type frameSink struct {
	buf     bytes.Buffer
	scr     *uv.TerminalRenderer
	cellbuf uv.ScreenBuffer
}

// newFrameSink builds the sink the way bubbletea builds its renderer for an
// alt-screen program: true colour, absolute cursor addressing, scroll
// optimization on.
func newFrameSink(width, height int) *frameSink {
	s := &frameSink{cellbuf: uv.NewScreenBuffer(width, height)}
	s.scr = uv.NewTerminalRenderer(&s.buf, nil)
	s.scr.SetColorProfile(colorprofile.TrueColor)
	s.scr.SetRelativeCursor(false)
	s.scr.SetFullscreen(true)
	s.scr.SetTabStops(-1)
	s.scr.SetScrollOptim(true)
	return s
}

// emit runs one frame through the diff and returns how many bytes it produced,
// which is the number that reaches the terminal.
func (s *frameSink) emit(content string) int {
	s.buf.Reset()
	styled := uv.NewStyledString(content)
	s.cellbuf.Clear()
	styled.Draw(s.cellbuf, s.cellbuf.Bounds())
	s.scr.Render(s.cellbuf.RenderBuffer)
	_ = s.scr.Flush()
	return s.buf.Len()
}

// floodOS builds a host filled with bordered panes, each carrying a DOOM-fire
// frame. Bordered rather than tiled on purpose: a tiled pane is borderless and
// returns from renderWindowBox before the frame is drawn, which is why the
// compositor benchmark never exercised the border box at all.
func floodOS(tb testing.TB, n, cols, rows int) *OS {
	tb.Helper()
	m := &OS{
		Windows:          make([]*terminal.Window, 0, n),
		FocusedWindow:    0,
		WorkspaceFocus:   map[int]int{},
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		Width:            cols,
		Height:           rows,
		Mode:             TerminalMode,
	}

	// Tile by hand into a grid, leaving every pane bordered.
	gridCols := 1
	for gridCols*gridCols < n {
		gridCols++
	}
	gridRows := (n + gridCols - 1) / gridCols
	winW, winH := cols/gridCols, rows/gridRows

	rng := rand.New(rand.NewSource(1))
	for i := range n {
		win := newTestWindow(tb, fmt.Sprintf("flood-%d-%d", n, i), winW, winH)
		win.X, win.Y = (i%gridCols)*winW, (i/gridCols)*winH
		win.Width, win.Height = winW, winH
		win.Workspace = 1
		floodFrame(tb, win, rng, win.ContentWidth(), win.ContentHeight())
		m.Windows = append(m.Windows, win)
	}
	return m
}

// BenchmarkClientFrame is the whole client frame under a flood, split into the
// three parts it is actually made of, so the attribution comes out of the
// benchmark rather than out of a profile read over the top of it.
//
//   - "parse" is the guest's bytes going into the emulator, the VT half.
//   - "compose" is the emulator's cells coming out as a frame string, which is
//     what every other benchmark in this repo measures.
//   - "emit" is the diff against the last frame and the escape sequences it
//     writes, which is what none of them did.
//   - "whole" is all three back to back with no timer games, and is the number
//     that bounds the client's frame rate. It should be about the sum, and
//     saying so is the check that the three parts are the whole thing.
//
// The pane count is the axis that matters, and it is an axis nothing measured
// before: one pane is the fullscreen case the flood profile was taken in, two
// is the common split, nine is a grid the maintainer actually uses, and twenty
// is the question of whether any of this is per-pane or per-cell. The screen is
// the same size in all four, so a cost that tracks cells stays flat across them
// and a cost that tracks panes climbs.
//
// Every pane is repainted every frame, which is the worst case rather than the
// common one, and the emit half is where that shows most: a diff against a
// frame that changed everywhere has nothing to skip.
//
// Benchmark only, so it costs a normal CI test run nothing. Under -bench it is
// a few seconds per case at the default -benchtime.
func BenchmarkClientFrame(b *testing.B) {
	for _, n := range []int{1, 2, 9, 20} {
		m := floodOS(b, n, realCols, realRows)
		rng := rand.New(rand.NewSource(2))
		sink := newFrameSink(realCols, realRows)

		repaint := func() {
			for _, w := range m.Windows {
				floodFrame(b, w, rng, w.ContentWidth(), w.ContentHeight())
			}
		}
		dirty := func() {
			for _, w := range m.Windows {
				w.InvalidateCache()
				w.MarkContentDirty()
			}
		}

		b.Run(fmt.Sprintf("panes-%d/parse", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				repaint()
			}
		})

		b.Run(fmt.Sprintf("panes-%d/compose", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				dirty()
				_ = m.composeFrame()
			}
		})

		b.Run(fmt.Sprintf("panes-%d/emit", n), func(b *testing.B) {
			// Two real frames, so what is measured is a frame-to-frame diff
			// rather than a first paint.
			dirty()
			first := m.composeFrame()
			sink.emit(first)
			repaint()
			dirty()
			second := m.composeFrame()

			var out int
			b.ReportAllocs()
			for b.Loop() {
				out = sink.emit(second)
				// Alternate, because emitting the same frame twice measures the
				// diff finding nothing.
				second, first = first, second
			}
			b.ReportMetric(float64(out), "bytes/frame")
		})

		b.Run(fmt.Sprintf("panes-%d/whole", n), func(b *testing.B) {
			var out int
			b.ReportAllocs()
			for b.Loop() {
				repaint()
				dirty()
				out = sink.emit(m.composeFrame())
			}
			b.ReportMetric(float64(out), "bytes/frame")
		})
	}
}

// TestFrameSinkEmitsTheFrame is what stops the benchmark above measuring
// nothing. A diff-and-emit that silently declined every frame would look
// wonderfully fast, so this pins that a changed frame produces bytes, that an
// unchanged one produces far fewer, and that the sink's own idea of the screen
// tracks what it was given.
func TestFrameSinkEmitsTheFrame(t *testing.T) {
	m := floodOS(t, 1, 80, 24)
	sink := newFrameSink(80, 24)

	first := m.composeFrame()
	if n := sink.emit(first); n == 0 {
		t.Fatal("the first frame emitted no bytes")
	}
	repeat := sink.emit(first)

	for _, w := range m.Windows {
		floodFrame(t, w, rand.New(rand.NewSource(3)), w.ContentWidth(), w.ContentHeight())
		w.InvalidateCache()
		w.MarkContentDirty()
	}
	changed := sink.emit(m.composeFrame())

	if repeat >= changed {
		t.Errorf("an unchanged frame emitted %d bytes and a repainted one %d; the diff is not diffing",
			repeat, changed)
	}
}
