package plot

import (
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// chartCase is one chart at one size, drawn in every mode.
type chartCase struct {
	name string
	w, h int
	draw func(w, h int, m Mode) *Grid
}

var pkgSecs = []float64{6.2, 4.8, 3.1, 1.4, 0.9}
var pkgNames = []string{"internal/session", "internal/app", "vt", "internal/tape", "internal/config"}
var runSecs = []float64{21.2, 20.8, 22.4, 19.9, 20.3, 23.8, 21.0, 20.6, 19.7, 20.1, 22.9, 21.4, 20.2, 19.8, 20.5, 24.1, 21.7, 20.9, 20.4, 19.6, 20.0, 21.3, 19.9, 20.7, 19.5, 20.2, 22.0, 20.6, 20.5, 18.4}

var cases = []chartCase{
	{"bar-h", 40, 5, func(w, h int, m Mode) *Grid {
		return Bar{Values: pkgSecs, Labels: pkgNames, Horizontal: true, Guide: true,
			Roles:  []Role{Error, Error, Error, Series2, Series2},
			Format: func(v float64) string { return Compact(v) + "s" }}.Draw(w, h, m)
	}},
	{"bar-v", 21, 4, func(w, h int, m Mode) *Grid {
		return Bar{Values: []float64{41, 39, 44, 40, 52, 47, 38}}.Draw(w, h, m)
	}},
	{"bar-v-labels", 20, 5, func(w, h int, m Mode) *Grid {
		return Bar{Values: []float64{3, 7, 5, 1}, Labels: []string{"mon", "tue", "wed", "thu"}}.Draw(w, h, m)
	}},
	{"bar-v-dense", 12, 3, func(w, h int, m Mode) *Grid {
		return Bar{Values: runSecs, Max: 25}.Draw(w, h, m)
	}},
	{"line", 30, 6, func(w, h int, m Mode) *Grid {
		return Line{Series: []Series{{Values: runSecs}}}.Draw(w, h, m)
	}},
	{"line-axis-two", 34, 8, func(w, h int, m Mode) *Grid {
		return Line{Axis: true, Series: []Series{
			{Name: "ms", Values: []float64{20, 22, 25, 21, 30, 28, 26, 35}},
			{Name: "p95", Values: []float64{40, 41, 47, 44, 52, 60, 55, 58}},
		}}.Draw(w, h, m)
	}},
	{"line-gap", 20, 4, func(w, h int, m Mode) *Grid {
		nan := math.NaN()
		return Line{Series: []Series{{Values: []float64{1, 2, 3, nan, nan, 3, 2, 1, 2, 3, 4, 5, 4, 3, 2, 1, 2, 3, 4, 5, 6, 5, 4, 3, 2, 3, 4, 5, 6, 7, 8, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0}}}}.Draw(w, h, m)
	}},
	{"area", 24, 5, func(w, h int, m Mode) *Grid {
		return Line{Fill: true, Series: []Series{{Values: []float64{120, 135, 150, 148, 160, 190, 185, 170, 210, 205}, Role: Series3}}}.Draw(w, h, m)
	}},
	{"sparkline", 30, 1, func(w, h int, m Mode) *Grid {
		return Sparkline{Values: runSecs}.Draw(w, h, m)
	}},
	{"sparkline-short", 20, 1, func(w, h int, m Mode) *Grid {
		return Sparkline{Values: []float64{2, 5, 3, 8, 4, 9}, Role: Dim}.Draw(w, h, m)
	}},
	{"sparkline-bucketed", 10, 1, func(w, h int, m Mode) *Grid {
		return Sparkline{Values: runSecs, Min: 15, Max: 25}.Draw(w, h, m)
	}},
	{"sparkline-gaps", 12, 1, func(w, h int, m Mode) *Grid {
		nan := math.NaN()
		return Sparkline{Values: []float64{1, 2, nan, 4, math.Inf(1), 3, nan, 5, 1}}.Draw(w, h, m)
	}},
	{"text-edge", 7, 2, func(w, h int, m Mode) *Grid {
		g := NewGrid(w, h)
		g.Text(6, 0, "日本", Fg, false) // the first wide grapheme does not fit
		g.Text(0, 1, "ab日本語", Dim, false)
		return g
	}},
	{"gauge-line", 20, 1, func(w, h int, m Mode) *Grid {
		return Gauge{Value: 412, Max: 417, Role: Success}.Draw(w, h, m)
	}},
	{"gauge-blocks", 24, 1, func(w, h int, m Mode) *Grid {
		return Gauge{Value: 0.43, Style: GaugeBlocks, Label: "43% CPU"}.Draw(w, h, m)
	}},
	{"histogram", 24, 5, func(w, h int, m Mode) *Grid {
		var vals []float64
		for i := 0; i < 200; i++ {
			vals = append(vals, 10+20*math.Exp(-float64((i%40-12)*(i%40-12))/60)+float64(i%7))
		}
		return Histogram{Values: vals, Bins: 12, Format: func(v float64) string { return Compact(v) + "ms" }}.Draw(w, h, m)
	}},
	{"heatmap", 30, 6, func(w, h int, m Mode) *Grid {
		rows := [][]float64{{0, 1, 2, 3, 4, 5}, {5, 4, 3, 2, 1, 0}, {0, math.NaN(), 9, 9, 2, 1}}
		return Heatmap{Values: rows, RowLabels: []string{"mon", "tue", "wed"}, ColLabels: []string{"00h", "01h", "02h", "03h", "04h", "05h"}}.Draw(w, h, m)
	}},
	{"heatmap-half", 16, 3, func(w, h int, m Mode) *Grid {
		var rows [][]float64
		for r := 0; r < 6; r++ {
			rows = append(rows, []float64{float64(r), float64(r * 2), float64(10 - r)})
		}
		return Heatmap{Values: rows}.Draw(w, h, m)
	}},
	{"donut", 40, 8, func(w, h int, m Mode) *Grid {
		return Pie{Values: []float64{61, 18, 9, 4, 2}, Labels: []string{"Go", "TypeScript", "Zig", "Shell", "Nix"}, Donut: true}.Draw(w, h, m)
	}},
	{"pie", 24, 5, func(w, h int, m Mode) *Grid {
		return Pie{Values: []float64{3, 1, math.NaN(), -2}, Labels: []string{"a", "b", "c", "d"}}.Draw(w, h, m)
	}},
	{"bar-v-axis", 40, 8, func(w, h int, m Mode) *Grid {
		return Bar{Values: runSecs, Labels: []string{"30 ago"}, Axis: true, Role: Info}.Draw(w, h, m)
	}},
	{"empty", 10, 3, func(w, h int, m Mode) *Grid {
		return Line{}.Draw(w, h, m)
	}},
	{"all-missing", 10, 2, func(w, h int, m Mode) *Grid {
		return Bar{Values: []float64{math.NaN(), math.Inf(1)}}.Draw(w, h, m)
	}},
}

// TestGolden draws every case in every mode and compares the text and the
// role of each cell with testdata/golden/NAME.golden.
func TestGolden(t *testing.T) {
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var b strings.Builder
			for _, m := range []Mode{Braille, Blocks, ASCII} {
				g := c.draw(c.w, c.h, m)
				checkGrid(t, g, c.w, c.h, m)
				fmt.Fprintf(&b, "=== %s %dx%d\n", m, c.w, c.h)
				b.WriteString(dump(g))
			}
			path := filepath.Join("testdata", "golden", c.name+".golden")
			if *update {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if b.String() != string(want) {
				t.Errorf("%s differs from the golden file. Got:\n%s", c.name, b.String())
			}
		})
	}
}

// dump writes the text of a grid, framed so trailing blanks show, then the
// role of each cell as a letter with a legend.
func dump(g *Grid) string {
	var b strings.Builder
	letters := map[Role]byte{}
	next := byte('a')
	for y := 0; y < g.H; y++ {
		b.WriteByte('|')
		for x := 0; x < g.W; x++ {
			c := g.At(x, y)
			switch {
			case c.Width == 0:
			case c.Text == "":
				b.WriteByte(' ')
			default:
				b.WriteString(c.Text)
			}
		}
		b.WriteString("|\n")
	}
	b.WriteString("--- roles\n")
	for y := 0; y < g.H; y++ {
		b.WriteByte('|')
		for x := 0; x < g.W; x++ {
			c := g.At(x, y)
			if c.Text == "" && c.Width != 0 || c.Role == None {
				b.WriteByte(' ')
				continue
			}
			l, ok := letters[c.Role]
			if !ok {
				l = next
				letters[c.Role] = l
				next++
			}
			if c.Bold {
				l -= 'a' - 'A'
			}
			b.WriteByte(l)
		}
		b.WriteString("|\n")
	}
	var legend []string
	for r, l := range letters {
		legend = append(legend, fmt.Sprintf("%c=%s", l, r))
	}
	sort.Strings(legend)
	if len(legend) > 0 {
		b.WriteString(strings.Join(legend, " ") + " (upper case is bold)\n")
	}
	return b.String()
}

// checkGrid asserts the invariants every chart keeps: the size asked for,
// rows that are exactly W cells wide, and glyphs the mode allows.
func checkGrid(t *testing.T, g *Grid, w, h int, m Mode) {
	t.Helper()
	w, h = max(w, 0), max(h, 0)
	if g.W != w || g.H != h || len(g.Cells) != w*h {
		t.Fatalf("grid is %dx%d with %d cells, want %dx%d", g.W, g.H, len(g.Cells), w, h)
	}
	for y := 0; y < g.H; y++ {
		rowW := 0
		for x := 0; x < g.W; x++ {
			c := g.At(x, y)
			switch c.Width {
			case 0:
				if x == 0 || g.At(x-1, y).Width != 2 {
					t.Fatalf("row %d: a continuation cell at %d follows no wide cell", y, x)
				}
			case 1, 2:
				if c.Text != "" && width(c.Text) != int(c.Width) {
					t.Fatalf("row %d col %d: %q is %d wide, the cell says %d", y, x, c.Text, width(c.Text), c.Width)
				}
				rowW += int(c.Width)
			default:
				t.Fatalf("row %d col %d: width %d", y, x, c.Width)
			}
			// Labels and values are the caller's text; the marks plot draws
			// itself have the other roles.
			if m == ASCII && c.Role != Fg && c.Role != Dim && c.Role != Mute {
				for _, r := range c.Text {
					if r > 0x7e || r < 0x20 {
						t.Fatalf("row %d col %d: %q is not ASCII", y, x, c.Text)
					}
				}
			}
			if m == Blocks {
				for _, r := range c.Text {
					if r >= 0x2800 && r <= 0x28ff {
						t.Fatalf("row %d col %d: braille %q in blocks mode", y, x, c.Text)
					}
				}
			}
		}
		if rowW != g.W {
			t.Fatalf("row %d is %d cells wide, want %d", y, rowW, g.W)
		}
	}
}

// TestFormat pins the number formats the labels use.
func TestFormat(t *testing.T) {
	for _, c := range []struct {
		v    float64
		want string
	}{{412, "412"}, {6.2, "6.2"}, {0.35, "0.35"}, {12345, "12.3k"}, {3.4e6, "3.4M"}, {math.Copysign(0, -1), "0"}, {math.NaN(), "-"}, {1e300, "1e+300"}, {0.0001, "0.0001"}} {
		if got := Compact(c.v); got != c.want {
			t.Errorf("Compact(%v) = %q, want %q", c.v, got, c.want)
		}
	}
	if got := Percent(0.988); got != "98.8%" {
		t.Errorf("Percent(0.988) = %q", got)
	}
	if got := Percent(1); got != "100%" {
		t.Errorf("Percent(1) = %q", got)
	}
}

// TestHugeBins asks a histogram for 1e10 bins, as a bound attribute could.
// The bins size an allocation, so plot caps them: before the cap this asked
// for 80 GB and the Go runtime ended the process.
func TestHugeBins(t *testing.T) {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	g := Histogram{Values: []float64{1, 2, 3}, Bins: 1e10}.Draw(40, 6, Braille)
	counts, _, _ := Histogram{Values: []float64{1, 2, 3}}.Counts(1e10)
	runtime.ReadMemStats(&after)
	checkGrid(t, g, 40, 6, Braille)
	if len(counts) > maxBins {
		t.Errorf("Counts made %d bins", len(counts))
	}
	if used := after.TotalAlloc - before.TotalAlloc; used > 1<<20 {
		t.Errorf("1e10 bins allocated %d KB", used>>10)
	}
	if NewGrid(1<<30, 1<<30).W != MaxSide {
		t.Error("NewGrid does not cap its size")
	}
}
