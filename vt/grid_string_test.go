package vt

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// linesText is the text uv.Line.String gives each row, joined with newlines,
// a nil row empty.
func linesText(rows []uv.Line) string {
	var b strings.Builder
	for y, row := range rows {
		if y > 0 {
			b.WriteByte('\n')
		}
		if row != nil {
			b.WriteString(row.String())
		}
	}
	return b.String()
}

// TestGridStringMatchesTheLines holds grid.String to the text uv.Line.String
// gives each row, joined with newlines, a nil row empty. Random grids mix the
// grid test's random cells with every cell that uv.Line.String treats
// differently from its neighbours, before content and in the blanks and zero
// cells a row ends in.
func TestGridStringMatchesTheLines(t *testing.T) {
	plain := func(s string) uv.Cell { return uv.Cell{Content: s, Width: 1} }
	// cases are the cells uv.Line.String treats differently from their neighbours.
	cases := [][]uv.Cell{
		{plain("x")},   // a row of text
		{uv.EmptyCell}, // a blank
		{{Content: " ", Width: 1, Style: uv.Style{Fg: ansi.BasicColor(1)}}},                 // a blank with a foreground
		{{Content: " ", Width: 1, Style: uv.Style{Bg: ansi.IndexedColor(17)}}},              // a blank with a background
		{{Content: " ", Width: 1, Style: uv.Style{UnderlineColor: ansi.IndexedColor(200)}}}, // a blank with only an underline colour
		{{Content: " ", Width: 1, Style: uv.Style{Attrs: uv.AttrBold}}},                     // a blank with an attribute
		{{Content: " ", Width: 1, Style: uv.Style{Underline: uv.UnderlineSingle}}},          // a blank with an underline
		{{Content: " ", Width: 1, Link: uv.Link{URL: "https://example.com"}}},               // a blank with a URL
		{{Content: " ", Width: 1, Link: uv.Link{Params: "id=1"}}},                           // a blank with only link params
		{{}},                           // a zero cell
		{{Content: "", Width: 1}},      // an unstyled empty narrow cell
		{{Content: "漢", Width: 2}, {}}, // a wide cell and its placeholder
		{{Content: "", Width: 0, Style: uv.Style{Fg: ansi.BasicColor(2)}}}, // a styled width-0 cell
		{plain("é")}, // an accent
		{{Content: "👨‍👩‍👧", Width: 2}, {}}, // a ZWJ cluster
	}

	check := func(t *testing.T, what string, g *grid) {
		t.Helper()
		if bad := extentHolds(g); bad != "" {
			t.Fatalf("%s: the fixture breaks the extent: %s", what, bad)
		}
		if got, want := g.String(), linesText(g.rows); got != want {
			t.Fatalf("%s: grid.String() = %q, the lines give %q", what, got, want)
		}
	}
	// line fills a row of the grid's width from the given cells, padding the
	// rest with tail.
	line := func(width int, tail func(x int) uv.Cell, cells ...uv.Cell) uv.Line {
		l := make(uv.Line, width)
		for x := range l {
			if x < len(cells) {
				l[x] = cells[x]
			} else {
				l[x] = tail(x)
			}
		}
		return l
	}
	blanks := func(int) uv.Cell { return uv.EmptyCell }

	t.Run("an empty grid", func(t *testing.T) {
		check(t, "no rows", newGrid(5, 0))
		check(t, "only nil rows", newGrid(5, 3))
	})

	t.Run("random grids", func(t *testing.T) {
		// cell mixes the grid test's random cells with every case above.
		cell := func(rng *rand.Rand) []uv.Cell {
			if rng.Intn(2) == 0 {
				c := randomGridCell(rng)
				if c == nil {
					return []uv.Cell{uv.EmptyCell}
				}
				return []uv.Cell{*c}
			}
			return cases[rng.Intn(len(cases))]
		}
		for seed := range int64(3000) {
			rng := rand.New(rand.NewSource(seed))
			w, h := 1+rng.Intn(210), 1+rng.Intn(60)
			g := newGrid(w, h)
			for y := range h {
				if rng.Intn(6) == 0 {
					continue
				}
				// A third of the rows end in blanks and zero cells from a
				// random column, as a line of text on a wide screen does.
				tailFrom := w
				if rng.Intn(3) == 0 {
					tailFrom = rng.Intn(w + 1)
				}
				var cells []uv.Cell
				for len(cells) < tailFrom {
					cells = append(cells, cell(rng)...)
				}
				for len(cells) < w {
					if rng.Intn(2) == 0 {
						cells = append(cells, uv.Cell{})
					} else {
						cells = append(cells, uv.EmptyCell)
					}
				}
				g.rows[y] = line(w, blanks, cells[:w]...)
				// The extent is the tightest the grid allows: the column
				// after the last cell that is not a plain blank.
				for x := range w {
					if !isBlankCell(&g.rows[y][x]) {
						g.ext[y] = x + 1
					}
				}
			}
			check(t, fmt.Sprintf("seed %d, %dx%d", seed, w, h), g)
		}
	})
}

// TestGridStringBudget pins the cost of reading a full screen of text, which
// a tape waiting on text pays on every poll.
func TestGridStringBudget(t *testing.T) {
	const cols, rows = 207, 55
	screen := func() string {
		var b strings.Builder
		for y := range rows {
			if y > 0 {
				b.WriteString("\r\n")
			}
			n := 60 + (y*53)%(cols-60)
			for x := range n {
				if y%3 == 0 && x == n/3 {
					fmt.Fprintf(&b, "\x1b[1;38;5;%dm", 16+y)
				}
				if y%3 == 0 && x == n/2 {
					b.WriteString("\x1b[m")
				}
				b.WriteByte(byte(' ' + (x*7+y*13)%95))
			}
		}
		return b.String()
	}
	e := NewEmulator(cols, rows)
	e.WriteString(screen())
	got := testing.AllocsPerRun(50, func() { _ = e.String() })
	t.Logf("Emulator.String on a %dx%d screen of text allocates %.0f times", cols, rows, got)
	if got > 40 {
		t.Errorf("Emulator.String on a %dx%d screen of text allocates %.0f times, want at most 40", cols, rows, got)
	}
}

// BenchmarkEmulatorString measures reading the screen of TestGridStringBudget
// as text.
func BenchmarkEmulatorString(b *testing.B) {
	const cols, rows = 207, 55
	screen := func() string {
		var sb strings.Builder
		for y := range rows {
			if y > 0 {
				sb.WriteString("\r\n")
			}
			n := 60 + (y*53)%(cols-60)
			for x := range n {
				if y%3 == 0 && x == n/3 {
					fmt.Fprintf(&sb, "\x1b[1;38;5;%dm", 16+y)
				}
				if y%3 == 0 && x == n/2 {
					sb.WriteString("\x1b[m")
				}
				sb.WriteByte(byte(' ' + (x*7+y*13)%95))
			}
		}
		return sb.String()
	}
	e := NewEmulator(cols, rows)
	e.WriteString(screen())
	b.ReportAllocs()
	for b.Loop() {
		_ = e.String()
	}
}
