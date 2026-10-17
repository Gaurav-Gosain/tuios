// Package shot renders a grid of terminal cells to PNG, SVG, HTML, ANSI or
// plain text, with an optional frame around it.
//
// A Grid holds resolved cells: each one has its text, its colours as RGB and
// its attributes. Build one cell by cell with NewGrid, or read a terminal
// emulator's screen with FromScreen. Render then turns the grid into the
// bytes of one format. A render is one call from grid to bytes, with no
// goroutines and no timers.
//
// PNG draws text with Frame.Font, then each face of Frame.Fallback, then the
// Go Mono it embeds. Box drawing, block elements, braille and powerline
// glyphs are drawn as shapes, so they join up whatever the font. A glyph no
// face has draws as a dotted box. MissingGlyphs lists those glyphs, so a
// caller with a font database can add fallback faces for them.
package shot

import (
	"fmt"
	"image/color"
	"math"
)

// Color is a resolved sRGB color. Alpha is kept because a frame background
// can be transparent; cell colors are always opaque by the time they land
// here.
type Color = color.RGBA

// RGB builds an opaque Color.
func RGB(r, g, b uint8) Color { return Color{R: r, G: g, B: b, A: 255} }

// Hex formats c as #rrggbb. Alpha is dropped: every place this string goes
// (SVG, HTML, SGR) carries opacity out of band or not at all.
func Hex(c Color) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// ParseHex parses #rrggbb or rrggbb. It reports false for anything else.
func ParseHex(s string) (Color, bool) {
	if len(s) > 0 && s[0] == '#' {
		s = s[1:]
	}
	if len(s) != 6 {
		return Color{}, false
	}
	var r, g, b uint8
	if _, err := fmt.Sscanf(s, "%02x%02x%02x", &r, &g, &b); err != nil {
		return Color{}, false
	}
	return RGB(r, g, b), true
}

// mix blends a toward b by t in [0,1], per channel in sRGB. Good enough for
// deriving wash stops and chrome tints; this is styling, not color science.
func mix(a, b Color, t float64) Color {
	lerp := func(x, y uint8) uint8 {
		return uint8(float64(x) + (float64(y)-float64(x))*t + 0.5)
	}
	return Color{R: lerp(a.R, b.R), G: lerp(a.G, b.G), B: lerp(a.B, b.B), A: 255}
}

// luma is the WCAG relative luminance of c.
func luma(c Color) float64 {
	lin := func(v uint8) float64 {
		f := float64(v) / 255
		if f <= 0.04045 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// Underline styles, matching the SGR 4:n space the emulator preserves.
type Underline uint8

const (
	UnderlineNone Underline = iota
	UnderlineSingle
	UnderlineDouble
	UnderlineCurly
	UnderlineDotted
	UnderlineDashed
)

// Cell is one resolved grid cell. Colors are concrete RGB: theme lookup,
// xterm fallback, reverse video, and the cursor are all folded in before a
// Cell exists, so every backend renders the same truth without knowing where
// it came from.
type Cell struct {
	// Cluster is the grapheme cluster, empty for a blank cell and for the
	// continuation column of a wide cluster.
	Cluster string
	// Width is the cluster's cell width: 1 or 2. 0 marks the continuation
	// column of a wide cluster, which no backend draws.
	Width uint8

	FG, BG Color
	// BGDefault marks a cell whose background is the grid default, so
	// backends can skip its rect and let the card ground show through.
	BGDefault bool

	Bold, Faint, Italic, Strike bool
	Underline                   Underline
	// Link is the OSC 8 target, kept so SVG and HTML can emit real anchors.
	Link string
}

// SameStyle reports whether two cells can share a text run.
func (c Cell) SameStyle(o Cell) bool {
	return c.FG == o.FG && c.BG == o.BG && c.BGDefault == o.BGDefault &&
		c.Bold == o.Bold && c.Faint == o.Faint && c.Italic == o.Italic &&
		c.Strike == o.Strike && c.Underline == o.Underline && c.Link == o.Link
}

// Grid is a captured screen region, fully resolved.
type Grid struct {
	Cols, Rows int
	// Cells is Rows rows of Cols cells each.
	Cells [][]Cell
	// FG and BG are the resolved default colors, used for the card ground
	// and for any styling derived from the content.
	FG, BG Color
	// Images are the pictures on the screen, drawn in order: a later image
	// in the same layer goes over an earlier one. See Image.
	Images []Image
}

// NewGrid allocates a blank grid with every cell on the default colors.
func NewGrid(cols, rows int, fg, bg Color) *Grid {
	g := &Grid{Cols: cols, Rows: rows, FG: fg, BG: bg}
	g.Cells = make([][]Cell, rows)
	for y := range g.Cells {
		row := make([]Cell, cols)
		for x := range row {
			row[x] = Cell{Width: 1, FG: fg, BG: bg, BGDefault: true}
		}
		g.Cells[y] = row
	}
	return g
}

// HasPrivateUse reports whether the grid holds a glyph from a private use area.
//
// That is what a Nerd Font icon is: a codepoint no standard font carries, put
// there by the font the terminal draws with. A capture full of them rendered in
// the embedded fallback face is a capture full of tofu boxes, and this is how
// the caller knows to say which setting fixes it rather than leaving the user
// to guess that the empty boxes are about a font at all.
func HasPrivateUse(g *Grid) bool {
	if g == nil {
		return false
	}
	for _, row := range g.Cells {
		for _, c := range row {
			for _, r := range c.Cluster {
				switch {
				case r >= 0xE000 && r <= 0xF8FF,
					r >= 0xF0000 && r <= 0xFFFFD,
					r >= 0x100000 && r <= 0x10FFFD:
					return true
				}
			}
		}
	}
	return false
}
