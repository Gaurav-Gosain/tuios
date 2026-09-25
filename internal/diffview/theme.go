package diffview

import (
	"image/color"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/charmtone"
)

// Palette is what a Theme is built from: the ground the diff is drawn on,
// the text colour, and the colours that mean something.
type Palette struct {
	// Ground is the background of everything the view draws.
	Ground color.Color
	// Fg is the colour of plain text.
	Fg color.Color
	// Accent tints the row under the cursor.
	Accent color.Color
	// Add and Delete tint added and removed lines.
	Add, Delete color.Color
	// Syntax is each class's colour. A nil entry is drawn in Fg.
	Syntax [NumClasses]color.Color
}

// How strongly each ground is tinted, as the share of the tint mixed into
// the ground. A share rather than a fixed colour, so a dark ground gets a
// dark green and a light one a pale green from the same theme colour.
const (
	tintLine   = 0.16
	tintGutter = 0.26
	tintWord   = 0.36
	tintCursor = 0.24
	tintQuiet  = 0.05
)

// Contrast floors. Code is text and clears the floor text does. A comment
// and a line number are read less closely and hold more of their hue at the
// floor a mark is held to.
const (
	floorCode  = overlay.ContrastFloor
	floorQuiet = overlay.MarkFloor
)

// Theme is a Palette worked out into the sequences every cell is drawn
// with: a ground per kind of line, the cursor's version of each, and each
// class's ink measured against the ground it sits on so it stays legible on
// a tinted line in any theme. It is built once per palette; drawing a row
// only looks sequences up.
type Theme struct {
	pal Palette
	// bg is the ground of a line's code, by kind, cursor and whether the
	// run is inside the part of the line that changed.
	bg [numKinds][2][2]color.Color
	// code is the sequence a run of each class starts with.
	code [numKinds][2][2][NumClasses]string
	// gutterBg and gutter are the line number column's ground and sequence.
	gutterBg [numKinds][2]color.Color
	gutter   [numKinds][2]string
	// sign is the sequence of the "+" or "-" before the code.
	sign [numKinds][2]string
}

// NewTheme works a palette out into a Theme. A nil colour in the palette
// falls back to a readable default, so a theme missing a slot still draws.
func NewTheme(p Palette) *Theme {
	if p.Ground == nil {
		p.Ground = charmtone.Char
	}
	p.Ground = solid(p.Ground)
	if p.Fg == nil {
		p.Fg = overlay.ContrastText(p.Ground)
	}
	p.Fg = overlay.Readable(solid(p.Fg), p.Ground)
	if p.Accent == nil {
		p.Accent = charmtone.Charple
	}
	if p.Add == nil {
		p.Add = charmtone.Julep
	}
	if p.Delete == nil {
		p.Delete = charmtone.Cherry
	}
	p.Accent, p.Add, p.Delete = solid(p.Accent), solid(p.Add), solid(p.Delete)

	t := &Theme{pal: p}
	quiet := overlay.MixColors(p.Ground, p.Fg, tintQuiet)
	for k := range numKinds {
		var line, word, gutter color.Color
		switch Kind(k) {
		case Add:
			line = overlay.MixColors(p.Ground, p.Add, tintLine)
			word = overlay.MixColors(p.Ground, p.Add, tintWord)
			gutter = overlay.MixColors(p.Ground, p.Add, tintGutter)
		case Delete:
			line = overlay.MixColors(p.Ground, p.Delete, tintLine)
			word = overlay.MixColors(p.Ground, p.Delete, tintWord)
			gutter = overlay.MixColors(p.Ground, p.Delete, tintGutter)
		case Missing:
			line, word, gutter = quiet, quiet, quiet
		default:
			line, word, gutter = p.Ground, p.Ground, quiet
		}
		for c := range 2 {
			l, w, g := line, word, gutter
			if c == 1 {
				l = overlay.MixColors(l, p.Accent, tintCursor)
				w = overlay.MixColors(w, p.Accent, tintCursor)
				g = overlay.MixColors(g, p.Accent, tintCursor+0.1)
			}
			t.bg[k][c][0], t.bg[k][c][1] = l, w
			t.gutterBg[k][c] = g
			for wi, bg := range []color.Color{l, w} {
				for class := range NumClasses {
					t.code[k][c][wi][class] = t.classSeq(Class(class), bg)
				}
			}
			numFg := overlay.ReadableAt(overlay.MixColors(p.Fg, g, 0.45), g, floorQuiet)
			signFg := p.Fg
			switch Kind(k) {
			case Add:
				numFg = overlay.ReadableAt(p.Add, g, floorQuiet)
				signFg = overlay.ReadableAt(p.Add, l, floorQuiet)
			case Delete:
				numFg = overlay.ReadableAt(p.Delete, g, floorQuiet)
				signFg = overlay.ReadableAt(p.Delete, l, floorQuiet)
			}
			t.gutter[k][c] = seq(numFg, g, false, false)
			t.sign[k][c] = seq(signFg, l, true, false)
		}
	}
	return t
}

// classSeq is the sequence for a class's ink on bg.
func (t *Theme) classSeq(class Class, bg color.Color) string {
	fg := t.pal.Syntax[class]
	if fg == nil {
		fg = t.pal.Fg
	}
	floor := floorCode
	italic, bold := false, false
	switch class {
	case Comment, Meta:
		floor, italic = floorQuiet, true
	case Heading:
		bold = true
	}
	return seq(overlay.ReadableAt(solid(fg), bg, floor), bg, bold, italic)
}

// Bg is the ground of a line's code: of the part that changed with word.
func (t *Theme) Bg(kind Kind, cursor, word bool) color.Color {
	return t.bg[kind][b2i(cursor)][b2i(word)]
}

// GutterBg is the ground of a line's number column.
func (t *Theme) GutterBg(kind Kind, cursor bool) color.Color {
	return t.gutterBg[kind][b2i(cursor)]
}

// Ground is the ground the theme was built on.
func (t *Theme) Ground() color.Color { return t.pal.Ground }

// Fg is the colour of plain text, as the theme draws it on its ground.
func (t *Theme) Fg() color.Color { return t.pal.Fg }

// SyntaxFromANSI maps a terminal theme's sixteen colours onto the classes,
// the way most editor themes built on a terminal palette do: keywords in
// magenta, strings in green, numbers in yellow, functions in blue, types in
// cyan and comments in the dim grey. A nil slot leaves its class plain.
func SyntaxFromANSI(pal [16]color.Color) [NumClasses]color.Color {
	var s [NumClasses]color.Color
	s[Keyword] = pal[5]
	s[Type] = pal[6]
	s[Func] = pal[4]
	s[Builtin] = pal[6]
	s[String] = pal[2]
	s[Number] = pal[3]
	s[Comment] = pal[8]
	s[Preproc] = pal[3]
	s[Tag] = pal[1]
	s[Attr] = pal[3]
	s[Heading] = pal[4]
	s[Meta] = pal[8]
	return s
}

// DefaultSyntax is the classes' colours when no terminal theme is set, from
// the charmtone palette the rest of tuios's chrome is drawn in.
func DefaultSyntax() [NumClasses]color.Color {
	var s [NumClasses]color.Color
	s[Keyword] = charmtone.Mauve
	s[Type] = charmtone.Guppy
	s[Func] = charmtone.Malibu
	s[Builtin] = charmtone.Turtle
	s[String] = charmtone.Cumin
	s[Number] = charmtone.Tang
	s[Comment] = charmtone.Squid
	s[Operator] = charmtone.Salmon
	s[Preproc] = charmtone.Bengal
	s[Tag] = charmtone.Mauve
	s[Attr] = charmtone.Hazy
	s[Heading] = charmtone.Malibu
	s[Meta] = charmtone.Squid
	return s
}

// seq is the SGR sequence that sets every attribute a cell can carry, so a
// run never inherits one from the run before it.
func seq(fg, bg color.Color, bold, italic bool) string {
	st := ansi.Style{}.Reset().ForegroundColor(solid(fg)).BackgroundColor(solid(bg))
	if bold {
		st = st.Bold()
	}
	if italic {
		st = st.Italic(true)
	}
	return st.String()
}

// solid is c as an opaque RGB colour. A basic or indexed ANSI colour would
// leave the host terminal to choose it, and the contrast measured against
// it would be a guess.
func solid(c color.Color) color.Color {
	if c == nil {
		return nil
	}
	r, g, b, _ := c.RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0xFF}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
