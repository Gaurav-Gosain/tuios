package vt_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// The existing emulator benchmarks all run at 80x24 and none report
// allocations. The maintainer runs 207x55, and the write path is the one that
// has to keep up with a command flooding output, so these measure it at the
// real size with allocation counts, on the output shapes that actually arrive.
const (
	perfCols = 207
	perfRows = 55
)

// BenchmarkEmulatorWriteHeavyOutput measures the parse-and-apply path under the
// output shapes a working day produces: a plain build log, a colourised one,
// and a full-screen application repainting itself.
func BenchmarkEmulatorWriteHeavyOutput(b *testing.B) {
	// A plain log line, the shape of a compiler or test runner scrolling past.
	plain := []byte(strings.Repeat("compiling package github.com/example/project/internal/thing\r\n", 32))

	// The same volume with per-line colour, the shape of most modern tooling.
	var colored strings.Builder
	for i := range 32 {
		fmt.Fprintf(&colored, "\x1b[38;5;%dmok\x1b[m   github.com/example/project/pkg%02d\t0.0%02ds\r\n",
			32+(i%6), i, i%10)
	}
	coloredBytes := []byte(colored.String())

	// A full-screen repaint: absolute cursor positioning per row and a styled
	// run on each, which is what an editor or a dashboard emits per frame.
	var repaint strings.Builder
	repaint.WriteString("\x1b[H")
	for y := 1; y <= perfRows; y++ {
		fmt.Fprintf(&repaint, "\x1b[%d;1H\x1b[48;5;%dm\x1b[38;5;15m%s\x1b[m",
			y, 16+(y%200), strings.Repeat("x", perfCols-1))
	}
	repaintBytes := []byte(repaint.String())

	cases := []struct {
		name string
		data []byte
	}{
		{"plain-log", plain},
		{"colored-log", coloredBytes},
		{"fullscreen-repaint", repaintBytes},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			emu := vt.NewEmulator(perfCols, perfRows)
			b.ReportAllocs()
			b.SetBytes(int64(len(tc.data)))
			b.ResetTimer()
			for b.Loop() {
				_, _ = emu.Write(tc.data)
			}
		})
	}
}

// BenchmarkEmulatorShortLineScroll is the flood case, and the one that decides
// how a multiplexer feels while a pane is dumping output.
//
// It differs from BenchmarkEmulatorScrollThroughput only in line length, and
// that is the whole point: a long line amortises the scroll over the printing,
// a short one does not. `yes`, a build log and a test runner all print a dozen
// characters and a newline, so the screen scrolls once every few graphemes and
// the per-scroll cost, rather than the per-character cost, is what the pane is
// paying. A CPU profile of the real client with three panes flooding put half
// of all time in the scroll path, which the long-line benchmark cannot see.
func BenchmarkEmulatorShortLineScroll(b *testing.B) {
	line := []byte("tuiosflood\r\n")

	b.Run("with-scrollback", func(b *testing.B) {
		emu := vt.NewEmulator(perfCols, perfRows)
		emu.SetScrollbackMaxLines(10000)
		b.ReportAllocs()
		b.SetBytes(int64(len(line)))
		b.ResetTimer()
		for b.Loop() {
			_, _ = emu.Write(line)
		}
	})

	b.Run("alt-screen-no-scrollback", func(b *testing.B) {
		emu := vt.NewEmulator(perfCols, perfRows)
		_, _ = emu.Write([]byte("\x1b[?1049h"))
		b.ReportAllocs()
		b.SetBytes(int64(len(line)))
		b.ResetTimer()
		for b.Loop() {
			_, _ = emu.Write(line)
		}
	})
}

// BenchmarkEmulatorScrollThroughput measures sustained scrolling, where every
// line written pushes one into scrollback. Scrollback retention is what makes
// this different from a plain write: the cost per line includes moving a line
// out of the active grid and into the ring.
func BenchmarkEmulatorScrollThroughput(b *testing.B) {
	line := []byte(strings.Repeat("output line with some length to it ", 5) + "\r\n")

	b.Run("with-scrollback", func(b *testing.B) {
		emu := vt.NewEmulator(perfCols, perfRows)
		emu.SetScrollbackMaxLines(10000)
		b.ReportAllocs()
		b.SetBytes(int64(len(line)))
		b.ResetTimer()
		for b.Loop() {
			_, _ = emu.Write(line)
		}
	})

	// The alternate screen has no scrollback, so this isolates the write cost
	// from the retention cost.
	b.Run("alt-screen-no-scrollback", func(b *testing.B) {
		emu := vt.NewEmulator(perfCols, perfRows)
		_, _ = emu.Write([]byte("\x1b[?1049h"))
		b.ReportAllocs()
		b.SetBytes(int64(len(line)))
		b.ResetTimer()
		for b.Loop() {
			_, _ = emu.Write(line)
		}
	})
}
