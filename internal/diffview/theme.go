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
	// Depth is the colour depth the diff is drawn for. It has to be the depth
	// overlay is set to, because the inks are measured as that depth shows
	// them.
	Depth overlay.Depth
	// Ground is the background of everything the view draws.
	Ground color.Color
	// Fg is the colour of plain text.
	Fg color.Color
	// Accent tints the row under the cursor.
	Accent color.Color
	// Add and Delete tint added and removed lines.
	Add, Delete color.Color
	// Syntax is each class's colour. A nil entry is drawn in Fg. At 16
	// colours it is not read: the classes take the terminal's own slots.
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

// The grounds at 256 colours. A tint mixed and then stepped down lands an
// added and a removed line on the same grey, so each is a fixed palette entry
// instead, the ones Codex uses: a dark green and a dark red on a dark ground,
// a pale green and a pale red on a light one, and the changed words one step
// brighter.
const (
	add256Dark      = ansi.IndexedColor(22)
	addWord256Dark  = ansi.IndexedColor(28)
	del256Dark      = ansi.IndexedColor(52)
	delWord256Dark  = ansi.IndexedColor(88)
	add256Light     = ansi.IndexedColor(194)
	addWord256Light = ansi.IndexedColor(157)
	del256Light     = ansi.IndexedColor(224)
	delWord256Light = ansi.IndexedColor(217)
)

// The slots the 16-colour diff takes its inks from. The terminal paints them
// from the user's own palette.
const (
	slotRed   = 1
	slotGreen = 2
	slotMuted = 8
)

// Contrast floors. Code is text and clears the floor text does. A comment
// and a line number are read less closely and hold more of their hue at the
// floor a mark is held to.
const (
	floorCode  = overlay.ContrastFloor
	floorQuiet = overlay.MarkFloor
)

// attrs are the attributes a run carries besides its colours.
type attrs uint8

const (
	attrBold attrs = 1 << iota
	attrItalic
	attrUnderline
)

// Theme is a Palette worked out into the sequences every cell is drawn
// with: a ground per kind of line, the cursor's version of each, and each
// class's ink measured against the ground it sits on so it stays legible on
// a tinted line in any theme. It is built once per palette; drawing a row
// only looks sequences up.
//
// What it draws depends on the depth. In truecolor the grounds are tints of
// the theme's colours. At 256 colours they are fixed palette entries. At 16
// colours there are no grounds at all: an added line's number and sign are
// green and a removed line's red, the changed words are bold and underlined,
// and the cursor is left to the caller, which reverses the gutter.
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

// grounds are the backgrounds of one kind of line: its code, the part of it
// that changed, and its number column.
type grounds struct {
	line, word, gutter color.Color
}

// NewTheme works a palette out into a Theme. A nil colour in the palette
// falls back to a readable default, so a theme missing a slot still draws.
func NewTheme(p Palette) *Theme {
	if p.Depth == overlay.Depth16 {
		return newTheme16(p)
	}
	if p.Ground == nil || overlay.IsNoColor(p.Ground) {
		p.Ground = charmtone.Char
	}
	p.Ground = solid(p.Ground)
	if p.Fg == nil || overlay.IsNoColor(p.Fg) {
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
	var g [numKinds][2]grounds
	if p.Depth == overlay.Depth256 {
		g = grounds256(p)
	} else {
		g = groundsTrueColor(p)
	}
	for k := range numKinds {
		for c := range 2 {
			l, w, gut := g[k][c].line, g[k][c].word, g[k][c].gutter
			t.bg[k][c][0], t.bg[k][c][1] = l, w
			t.gutterBg[k][c] = gut
			for wi, bg := range []color.Color{l, w} {
				for class := range NumClasses {
					t.code[k][c][wi][class] = t.classSeq(Class(class), bg)
				}
			}
			numFg := overlay.ReadableAt(overlay.MixColors(p.Fg, gut, 0.45), gut, floorQuiet)
			signFg := p.Fg
			switch Kind(k) {
			case Add:
				numFg = overlay.ReadableAt(p.Add, gut, floorQuiet)
				signFg = overlay.ReadableAt(p.Add, l, floorQuiet)
			case Delete:
				numFg = overlay.ReadableAt(p.Delete, gut, floorQuiet)
				signFg = overlay.ReadableAt(p.Delete, l, floorQuiet)
			}
			t.gutter[k][c] = seq(numFg, gut, 0)
			t.sign[k][c] = seq(signFg, l, attrBold)
		}
	}
	return t
}

// groundsTrueColor are the grounds in truecolor: each kind's colour mixed
// into the ground, and the accent mixed into those for the cursor.
func groundsTrueColor(p Palette) [numKinds][2]grounds {
	var g [numKinds][2]grounds
	quiet := overlay.MixColors(p.Ground, p.Fg, tintQuiet)
	for k := range numKinds {
		var base grounds
		switch Kind(k) {
		case Add:
			base = grounds{
				overlay.MixColors(p.Ground, p.Add, tintLine),
				overlay.MixColors(p.Ground, p.Add, tintWord),
				overlay.MixColors(p.Ground, p.Add, tintGutter),
			}
		case Delete:
			base = grounds{
				overlay.MixColors(p.Ground, p.Delete, tintLine),
				overlay.MixColors(p.Ground, p.Delete, tintWord),
				overlay.MixColors(p.Ground, p.Delete, tintGutter),
			}
		case Missing:
			base = grounds{quiet, quiet, quiet}
		default:
			base = grounds{p.Ground, p.Ground, quiet}
		}
		g[k][0] = base
		g[k][1] = grounds{
			overlay.MixColors(base.line, p.Accent, tintCursor),
			overlay.MixColors(base.word, p.Accent, tintCursor),
			overlay.MixColors(base.gutter, p.Accent, tintCursor+0.1),
		}
	}
	return g
}

// grounds256 are the grounds at 256 colours. Added and removed lines are
// fixed palette entries, picked for a dark or a light ground, and keep them
// under the cursor, where the number column takes the accent instead. A
// context line and the empty side of a split row are the ground and a step
// off it, both as the palette shows them.
func grounds256(p Palette) [numKinds][2]grounds {
	ground := overlay.Shown(p.Ground)
	light := overlay.ContrastRatio(ground, color.Black) > overlay.ContrastRatio(ground, color.White)
	add := grounds{add256Dark, addWord256Dark, add256Dark}
	del := grounds{del256Dark, delWord256Dark, del256Dark}
	if light {
		add = grounds{add256Light, addWord256Light, add256Light}
		del = grounds{del256Light, delWord256Light, del256Light}
	}
	quiet := overlay.Shown(overlay.MixColors(p.Ground, p.Fg, tintQuiet))
	if quiet == ground {
		// The quiet tint is finer than the palette: take the next step.
		if light {
			quiet = overlay.Shown(overlay.Darker(p.Ground, 1.12))
		} else {
			quiet = overlay.Shown(overlay.Lighter(p.Ground, 1.25))
		}
	}
	cursorLine := overlay.Shown(overlay.MixColors(p.Ground, p.Accent, tintCursor))
	cursorGutter := overlay.Shown(overlay.MixColors(quiet, p.Accent, tintCursor+0.1))

	var g [numKinds][2]grounds
	for k := range numKinds {
		switch Kind(k) {
		case Add:
			g[k][0], g[k][1] = add, grounds{add.line, add.word, cursorGutter}
		case Delete:
			g[k][0], g[k][1] = del, grounds{del.line, del.word, cursorGutter}
		case Missing:
			g[k][0] = grounds{quiet, quiet, quiet}
			g[k][1] = grounds{cursorLine, cursorLine, cursorGutter}
		default:
			g[k][0] = grounds{ground, ground, quiet}
			g[k][1] = grounds{cursorLine, cursorLine, cursorGutter}
		}
	}
	return g
}

// newTheme16 is the Theme at 16 colours. No ground is painted: every one is
// the terminal's own. The inks are slots the terminal paints from the user's
// palette, so nothing is measured, and what a ground said at the other depths
// is said by attributes and by the line numbers' colour. The cursor is the
// same as the rest; the caller reverses the gutter (see overlay.Palette.Mark).
func newTheme16(p Palette) *Theme {
	p.Ground, p.Fg = overlay.NoColor, overlay.NoColor
	p.Accent, p.Add, p.Delete = overlay.Slot(5), overlay.Slot(slotGreen), overlay.Slot(slotRed)
	var slots [16]color.Color
	for i := range slots {
		slots[i] = overlay.Slot(i)
	}
	p.Syntax = SyntaxFromANSI(slots)
	t := &Theme{pal: p}
	for k := range numKinds {
		num, sign := overlay.Slot(slotMuted), color.Color(nil)
		switch Kind(k) {
		case Add:
			num, sign = p.Add, p.Add
		case Delete:
			num, sign = p.Delete, p.Delete
		}
		for c := range 2 {
			t.bg[k][c] = [2]color.Color{overlay.NoColor, overlay.NoColor}
			t.gutterBg[k][c] = overlay.NoColor
			t.gutter[k][c] = seq(num, nil, 0)
			t.sign[k][c] = seq(sign, nil, attrBold)
			for class := range NumClasses {
				fg, a := p.Syntax[class], classAttrs(Class(class))
				t.code[k][c][0][class] = seq(fg, nil, a)
				t.code[k][c][1][class] = seq(fg, nil, a|attrBold|attrUnderline)
			}
		}
	}
	return t
}

// classAttrs are the attributes a class is drawn with at every depth.
func classAttrs(class Class) attrs {
	switch class {
	case Comment, Meta:
		return attrItalic
	case Heading:
		return attrBold
	}
	return 0
}

// classSeq is the sequence for a class's ink on bg.
func (t *Theme) classSeq(class Class, bg color.Color) string {
	fg := t.pal.Syntax[class]
	if fg == nil {
		fg = t.pal.Fg
	}
	floor := floorCode
	if class == Comment || class == Meta {
		floor = floorQuiet
	}
	return seq(overlay.ReadableAt(solid(fg), bg, floor), bg, classAttrs(class))
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
func seq(fg, bg color.Color, a attrs) string {
	st := ansi.Style{}.Reset().ForegroundColor(emit(fg)).BackgroundColor(emit(bg))
	if a&attrBold != 0 {
		st = st.Bold()
	}
	if a&attrItalic != 0 {
		st = st.Italic(true)
	}
	if a&attrUnderline != 0 {
		st = st.Underline(true)
	}
	return st.String()
}

// emit is c as seq writes it: nil for the terminal's default colour, a slot
// or a palette entry as the index it is, so the frame writer passes it on
// unchanged, and anything else as opaque RGB.
func emit(c color.Color) color.Color {
	if c == nil || overlay.IsNoColor(c) {
		return nil
	}
	switch c.(type) {
	case ansi.BasicColor, ansi.IndexedColor:
		return c
	}
	return solid(c)
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
