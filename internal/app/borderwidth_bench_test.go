package app

// What it costs to measure a box that has already been sized.
//
// A profile of the flood benchmark puts ansi.StringWidth at 43.75% cumulative,
// and one of the five passes over the content is tuios's own: addToBorder
// opens with
//
//	width := max(lipgloss.Width(content)-2, 0)
//
// where content is the whole rendered box. lipgloss.Width walks every row
// through a grapheme-cluster iterator to find the widest one, so a 40-row pane
// pays 40 full-width scans of styled text, per pane, per frame, to recover a
// number the caller already holds: the same call takes *terminal.Window, whose
// Width is the box's width by construction.
//
// These two benchmarks are the same question asked twice: what the scan costs,
// and what knowing the answer costs. The gap is the size of the saving, and
// once the call site reads the width off the window instead, the ratio here is
// what proves it stayed fixed.

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// benchBox builds a rendered pane box of a realistic size and style density:
// a border, and rows carrying colour changes the way a real frame does, so the
// width scan has escape sequences to step over rather than plain ASCII.
func benchBox(cols, rows int) string {
	var b strings.Builder
	b.Grow(cols * rows * 8)
	b.WriteString("╭" + strings.Repeat("─", cols-2) + "╮\n")
	for y := range rows - 2 {
		b.WriteString("│")
		// Three style runs per row is typical of a shell prompt plus output.
		seg := (cols - 2) / 3
		for i := range 3 {
			fmt.Fprintf(&b, "\x1b[38;5;%dm", 8+(y+i)%200)
			n := seg
			if i == 2 {
				n = (cols - 2) - 2*seg
			}
			b.WriteString(strings.Repeat("x", n))
		}
		b.WriteString("\x1b[m│\n")
	}
	b.WriteString("╰" + strings.Repeat("─", cols-2) + "╯")
	return b.String()
}

// BenchmarkBorderWidthMeasurement contrasts measuring a rendered box against
// reading the width that produced it.
//
// The sizes are the ones the flood benchmarks use: a single wide pane, and a
// tile from a nine-way split.
func BenchmarkBorderWidthMeasurement(b *testing.B) {
	for _, sz := range []struct {
		name       string
		cols, rows int
	}{
		{"pane-158x40", 158, 40},
		{"tile-69x18", 69, 18},
	} {
		box := benchBox(sz.cols, sz.rows)

		b.Run(sz.name+"/measured", func(b *testing.B) {
			b.ReportAllocs()
			var sink int
			for b.Loop() {
				sink = max(lipgloss.Width(box)-2, 0)
			}
			if sink == 0 {
				b.Fatal("the box measured zero columns")
			}
		})

		b.Run(sz.name+"/known", func(b *testing.B) {
			b.ReportAllocs()
			var sink int
			for b.Loop() {
				sink = max(sz.cols-2, 0)
			}
			if sink == 0 {
				b.Fatal("the box measured zero columns")
			}
		})
	}
}

// TestBorderWidthMeasurementAgrees is what makes the benchmark above an
// argument rather than a curiosity: the cheap answer has to be the same
// answer. If lipgloss.Width of a rendered box ever stops equalling the width
// it was built at, reading the width off the window would be wrong and this
// says so before anyone acts on the benchmark.
func TestBorderWidthMeasurementAgrees(t *testing.T) {
	for _, sz := range []struct{ cols, rows int }{
		{158, 40}, {69, 18}, {20, 5},
	} {
		box := benchBox(sz.cols, sz.rows)
		if got := lipgloss.Width(box); got != sz.cols {
			t.Errorf("a %dx%d box measures %d columns, want %d",
				sz.cols, sz.rows, got, sz.cols)
		}
	}
}
