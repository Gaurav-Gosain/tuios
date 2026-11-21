package app

import (
	"fmt"
	"math/rand"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

const (
	floodCols = 158
	floodRows = 40
)

var doomFirePalette = []int{
	0, 52, 88, 124, 160, 196, 202, 208, 214, 220, 226, 227, 228, 229, 230, 231,
}

var doomFireGlyphs = []string{"█", "▓", "▒", "░"}

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

func floodWindow(tb testing.TB, id string, cols, rows int) *terminal.Window {
	tb.Helper()
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
