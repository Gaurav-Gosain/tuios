// Package plot draws charts as terminal cells: bar, line, area, sparkline,
// gauge and histogram.
//
// A chart draws into a Grid of a given width and height. Each cell holds one
// grapheme and a Role, the name of a theme colour such as "series1" or
// "mute". The host maps roles to its own colours at its own colour depth, so
// the package holds no colour and writes no escape sequence.
//
// A Mode picks the glyphs: Braille uses braille dots for lines and areas and
// eighth blocks for bars, Blocks uses only block elements, and ASCII uses
// printable ASCII only. Pick the best mode the terminal and its font can
// show.
//
// Every chart is total. NaN and infinite values count as missing, empty data
// draws an empty grid, and a grid is always exactly the size asked for.
package plot

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Mode is the set of glyphs a chart may draw with.
type Mode uint8

// The modes, best first.
const (
	Braille Mode = iota // braille dots and block elements
	Blocks              // block elements only
	ASCII               // printable ASCII only
)

func (m Mode) String() string {
	switch m {
	case Braille:
		return "braille"
	case Blocks:
		return "blocks"
	}
	return "ascii"
}

// Role names a theme colour. The names are the TML roles, plus Track for the
// empty part of a gauge or a bar.
type Role string

// The roles a chart draws with.
const (
	None         Role = ""
	Fg           Role = "fg"
	Dim          Role = "dim"
	Mute         Role = "mute"
	Edge         Role = "edge"
	Track        Role = "track"
	Accent       Role = "accent"
	AccentBright Role = "accent-bright"
	Success      Role = "success"
	Warning      Role = "warning"
	Error        Role = "error"
	Info         Role = "info"
	Series1      Role = "series1"
	Series2      Role = "series2"
	Series3      Role = "series3"
	Series4      Role = "series4"
	Series5      Role = "series5"
	Series6      Role = "series6"
	// Heat1 to Heat5 are a ramp from low to high for a heatmap. They are
	// not TML roles: a host derives them, such as from the ground toward
	// the accent.
	Heat1 Role = "heat1"
	Heat2 Role = "heat2"
	Heat3 Role = "heat3"
	Heat4 Role = "heat4"
	Heat5 Role = "heat5"
)

var heat = [...]Role{Heat1, Heat2, Heat3, Heat4, Heat5}

var series = [...]Role{Series1, Series2, Series3, Series4, Series5, Series6}

// SeriesRole returns the role of series i, counting from 0. It cycles after
// six.
func SeriesRole(i int) Role {
	k := i % len(series)
	if k < 0 {
		k += len(series)
	}
	return series[k]
}

// Cell is one cell of a Grid. Text is one grapheme, or "" for a blank cell.
// Width is the grapheme's width in cells. A wide grapheme takes two cells:
// the second has Width 0 and no Text. Role colours the text. Bg, when set,
// colours the cell's background: a half block uses both to show two
// pixels in one cell.
type Cell struct {
	Text  string
	Width uint8
	Role  Role
	Bg    Role
	Bold  bool
}

// Grid is a chart's output: H rows of W cells.
type Grid struct {
	W, H  int
	Cells []Cell // row-major
}

// MaxSide is the largest width or height of a grid. A larger size is cut
// to it, so a caller cannot make a chart allocate without bound.
const MaxSide = 4096

// NewGrid returns a blank grid. A negative size counts as 0, and a size
// past MaxSide counts as MaxSide.
func NewGrid(w, h int) *Grid {
	w, h = min(max(w, 0), MaxSide), min(max(h, 0), MaxSide)
	g := &Grid{W: w, H: h, Cells: make([]Cell, w*h)}
	for i := range g.Cells {
		g.Cells[i].Width = 1
	}
	return g
}

// At returns the cell at x, y, or a blank cell outside the grid.
func (g *Grid) At(x, y int) Cell {
	if x < 0 || y < 0 || x >= g.W || y >= g.H {
		return Cell{Width: 1}
	}
	return g.Cells[y*g.W+x]
}

// set puts a one-cell glyph at x, y. Outside the grid it does nothing.
func (g *Grid) set(x, y int, text string, role Role) {
	if x < 0 || y < 0 || x >= g.W || y >= g.H {
		return
	}
	g.clearWide(x, y)
	g.Cells[y*g.W+x] = Cell{Text: text, Width: 1, Role: role}
}

// setBg puts a one-cell glyph with a background role at x, y.
func (g *Grid) setBg(x, y int, text string, role, bg Role) {
	if x < 0 || y < 0 || x >= g.W || y >= g.H {
		return
	}
	g.clearWide(x, y)
	g.Cells[y*g.W+x] = Cell{Text: text, Width: 1, Role: role, Bg: bg}
}

// clearWide blanks the other half of a wide grapheme that x, y is part of.
func (g *Grid) clearWide(x, y int) {
	c := g.Cells[y*g.W+x]
	if c.Width == 0 && x > 0 {
		g.Cells[y*g.W+x-1] = Cell{Width: 1}
	}
	if c.Width == 2 && x+1 < g.W {
		g.Cells[y*g.W+x+1] = Cell{Width: 1}
	}
}

// Text writes s from x on row y and returns the column after the last cell
// written. It stops at the right edge: a wide grapheme that does not fit is
// left out. Control characters are dropped.
func (g *Grid) Text(x, y int, s string, role Role, bold bool) int {
	if y < 0 || y >= g.H {
		return x
	}
	for s != "" {
		cl, w := ansi.FirstGraphemeCluster(s, ansi.GraphemeWidth)
		s = s[len(cl):]
		if w <= 0 || isControl(cl) {
			continue
		}
		if !utf8.ValidString(cl) {
			cl, w = "?", 1
		}
		if x+w > g.W {
			break
		}
		if x >= 0 {
			g.clearWide(x, y)
			g.Cells[y*g.W+x] = Cell{Text: cl, Width: uint8(w), Role: role, Bold: bold}
			if w == 2 {
				g.clearWide(x+1, y)
				g.Cells[y*g.W+x+1] = Cell{Width: 0, Role: role}
			}
		}
		x += w
	}
	return x
}

// TextRight writes s so that it ends at column right, exclusive.
func (g *Grid) TextRight(right, y int, s string, role Role, bold bool) {
	g.Text(right-width(s), y, s, role, bold)
}

// String returns the grid as text, one line per row, with trailing blanks
// cut.
func (g *Grid) String() string {
	var b strings.Builder
	for y := 0; y < g.H; y++ {
		var line strings.Builder
		for x := 0; x < g.W; x++ {
			c := g.Cells[y*g.W+x]
			switch {
			case c.Width == 0:
			case c.Text == "":
				line.WriteByte(' ')
			default:
				line.WriteString(c.Text)
			}
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		if y < g.H-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// width returns the width of s in cells, as Grid.Text counts it.
func width(s string) int {
	n := 0
	for s != "" {
		cl, w := ansi.FirstGraphemeCluster(s, ansi.GraphemeWidth)
		s = s[len(cl):]
		if w > 0 && !isControl(cl) {
			n += w
		}
	}
	return n
}

// truncate cuts s to at most n cells. A cut string ends in tail.
func truncate(s string, n int, tail string) string {
	if width(s) <= n {
		return s
	}
	tw := width(tail)
	if n < tw {
		return ""
	}
	var b strings.Builder
	used := 0
	for s != "" {
		cl, w := ansi.FirstGraphemeCluster(s, ansi.GraphemeWidth)
		s = s[len(cl):]
		if w <= 0 || isControl(cl) {
			continue
		}
		if used+w > n-tw {
			break
		}
		b.WriteString(cl)
		used += w
	}
	return b.String() + tail
}

func isControl(cl string) bool {
	if cl == "" {
		return true
	}
	r := []rune(cl)[0]
	return r < 0x20 || (r >= 0x7f && r < 0xa0)
}
