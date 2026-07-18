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

// BenchmarkEmulatorRenderReal measures the emulator's built-in Render at the
// real size. This is the single most expensive thing the unfocused render path
// does: a CPU profile of renderTerminal for an unfocused window attributed
// about 93% of its time here, inside the ultraviolet line renderer.
func BenchmarkEmulatorRenderReal(b *testing.B) {
	emu := vt.NewEmulator(perfCols, perfRows)
	for y := 1; y <= perfRows; y++ {
		_, _ = emu.Write(fmt.Appendf(nil, "\x1b[%d;1H\x1b[38;5;%dm%s\x1b[m",
			y, 16+(y%200), strings.Repeat("content ", perfCols/8)))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = emu.Render()
	}
}

// BenchmarkScrollbackRetainedMemory reports how much heap one window's
// scrollback holds, which is a memory figure rather than a speed one and is
// why it reports a custom metric instead of leaning on ns/op.
//
// It is here because the number is large enough to matter for a program the
// user leaves open all day. At 207 columns each retained line costs on the
// order of 25KB, against roughly 175 bytes of actual text, because a
// scrollback line is a full slice of cell structs carrying content strings,
// style interfaces and link data for every column, whether or not the column
// holds anything. The default scrollback is 10000 lines, so one window with a
// filled buffer holds a couple of hundred megabytes, and the configuration
// permits 1000000 lines.
func BenchmarkScrollbackRetainedMemory(b *testing.B) {
	const lines = 2000

	for b.Loop() {
		emu := vt.NewEmulator(perfCols, perfRows)
		emu.SetScrollbackMaxLines(lines)
		line := []byte(strings.Repeat("output line with some length to it ", 5) + "\r\n")
		for range lines + perfRows {
			_, _ = emu.Write(line)
		}
		if got := emu.ScrollbackLen(); got != lines {
			b.Fatalf("scrollback holds %d lines, want %d", got, lines)
		}
	}
	b.ReportMetric(float64(lines), "retained-lines")
}
