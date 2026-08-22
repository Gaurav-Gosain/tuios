package app

import (
	"fmt"
	"math/rand"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The flood profile that motivated this benchmark was taken at 158x40, which is
// the maintainer's terminal when a pane is fullscreen.
const (
	floodCols = 158
	floodRows = 40
)

// doomFirePalette is the colour ramp a DOOM fire demo cycles through: a black
// floor, then reds, oranges and yellows up to white. A flood repaints every
// cell of every row from this ramp, so neighbouring cells rarely share a style
// and the render loop flushes a styled batch every few columns.
var doomFirePalette = []int{
	0, 52, 88, 124, 160, 196, 202, 208, 214, 220, 226, 227, 228, 229, 230, 231,
}

// doomFireGlyphs are the block characters a fire demo shades its cells with.
// They are multi-byte and one column wide, which is the case a byte or a rune
// count gets wrong and the cell grid gets right.
var doomFireGlyphs = []string{"█", "▓", "▒", "░"}

// floodFrame writes one DOOM-fire frame into a window's emulator: a full
// repaint from home, every cell carrying a colour and a glyph from the ramp.
func floodFrame(tb testing.TB, win *terminal.Window, rng *rand.Rand, cols, rows int) {
	tb.Helper()
	buf := make([]byte, 0, cols*rows*16)
	for y := range rows {
		buf = fmt.Appendf(buf, "\x1b[%d;1H", y+1)
		last := -1
		for range cols {
			c := doomFirePalette[rng.Intn(len(doomFirePalette))]
			if c != last {
				buf = fmt.Appendf(buf, "\x1b[38;5;%dm", c)
				last = c
			}
			buf = append(buf, doomFireGlyphs[rng.Intn(len(doomFireGlyphs))]...)
		}
		buf = append(buf, "\x1b[m"...)
	}
	win.LockIO()
	_, _ = win.Terminal.Write(buf)
	win.UnlockIO()
}

// floodWindow builds a fullscreen-sized window carrying one DOOM-fire frame.
func floodWindow(tb testing.TB, id string, cols, rows int) *terminal.Window {
	tb.Helper()
	// The pane is two cells larger than its grid in each direction, which is
	// what a bordered pane holding a cols-by-rows emulator measures.
	win := newTestWindow(tb, id, cols+2, rows+2)
	floodFrame(tb, win, rand.New(rand.NewSource(1)), cols, rows)
	return win
}

// BenchmarkRenderWindowBoxFlood measures the bordered box around a pane under a
// DOOM-fire flood, which is where a CPU profile of the client put 40% of its
// samples. renderTerminal is inside it, so the two variants separate the box
// from the content: "content-cached" holds the pane's rendered content still
// and leaves only the box, which is the part this benchmark exists to watch.
func BenchmarkRenderWindowBoxFlood(b *testing.B) {
	border := lipgloss.Color("62")

	b.Run("dirty", func(b *testing.B) {
		win := floodWindow(b, "flood-dirty", floodCols, floodRows)
		m := newTestOS(win)
		m.Mode = TerminalMode
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			win.MarkContentDirty()
			_ = m.renderWindowBox(win, 0, true, border)
		}
	})

	b.Run("unfocused", func(b *testing.B) {
		win := floodWindow(b, "flood-unfocused", floodCols, floodRows)
		m := newTestOS(win)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			win.MarkContentDirty()
			_ = m.renderWindowBox(win, 0, false, border)
		}
	})

	b.Run("content-cached", func(b *testing.B) {
		win := floodWindow(b, "flood-cached", floodCols, floodRows)
		m := newTestOS(win)
		m.Mode = TerminalMode
		win.MarkContentDirty()
		_ = m.renderWindowBox(win, 0, true, border)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_ = m.renderWindowBox(win, 0, true, border)
		}
	})
}
