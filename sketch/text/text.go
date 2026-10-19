// Package text draws diagrams with box-drawing characters, for terminals that
// show no pictures. It takes the layout that sketch works out for the SVG, maps it
// to cells at a scale where every label fits, and writes the labels as real
// text. It is not a port of pele: pele draws only SVG.
//
// Flowcharts and sequence diagrams have a text drawing. Other types give
// ErrUnsupported.
package text

import (
	"errors"
	"strings"

	"tuios.dev/sketch"
)

// ErrUnsupported is returned for a diagram type with no text drawing.
var ErrUnsupported = errors.New("text: this diagram type has no text drawing")

// ErrTooWide is returned when the drawing needs more columns than Options.Cols.
var ErrTooWide = errors.New("text: the diagram is wider than the columns there are")

// ErrTooLarge is returned when the drawing would need more than MaxCells
// cells, whatever Options.Cols is.
var ErrTooLarge = errors.New("text: the diagram needs too many cells")

// MaxCells bounds the cells of one drawing: 4,000,000 cells is about 48 MB.
// A diagram at the source limits of sketch can ask for far more, for example a
// wide rank above a deep chain.
const MaxCells = 4_000_000

// Options say how to draw.
type Options struct {
	// Cols bounds the width in cells. 0 means no bound.
	Cols int
	// Color writes ANSI colours (16 colours, SGR 30 to 37 and 90 to 97) for
	// boxes, lines and notes. Without it the lines are plain text.
	Color bool
}

// Draw draws a parsed diagram as lines of text.
func Draw(d *sketch.Diagram, opt Options) ([]string, error) {
	if d.Type != "flowchart" && d.Type != "sequence" {
		return nil, ErrUnsupported
	}
	lines, err := draw(d, opt, sketch.RenderOptions{})
	if err == ErrTooWide {
		// pele draws narrower when it is told the width there is: a flowchart
		// that runs across turns to run down, and spacing tightens. A cell is
		// about 8 units of the layout.
		w := float64(opt.Cols) * 8
		turn := true
		lines, err = draw(d, opt, sketch.RenderOptions{MaxWidth: &w, AutoDirection: &turn, DirectionBreakpoint: &[]float64{w + 1}[0]})
	}
	return lines, err
}

func draw(d *sketch.Diagram, opt Options, ro sketch.RenderOptions) ([]string, error) {
	layout, err := d.Layout(ro)
	if err != nil {
		return nil, err
	}
	var c *canvas
	if d.Type == "flowchart" {
		c, err = drawFlowchart(layout, opt)
	} else {
		c, err = drawSequence(layout, opt)
	}
	if err != nil {
		return nil, err
	}
	return c.lines(opt.Color), nil
}

// Roles of a cell, which pick its colour.
const (
	roleNone = iota
	roleLine
	roleBox
	roleText
	roleNote
	roleFrame
	roleTitle
	roleHead
)

var roleSGR = [...]string{
	roleNone:  "",
	roleLine:  "90",
	roleBox:   "36",
	roleText:  "",
	roleNote:  "33",
	roleFrame: "90",
	roleTitle: "1",
	roleHead:  "",
}

// Directions of the strokes in a cell, for joining lines.
const (
	up    = 1
	right = 2
	down  = 4
	left  = 8
)

// Line weights.
const (
	light = iota
	dotted
	heavy
)

type cell struct {
	r    rune
	role uint8
	// mask holds the strokes of lines drawn through the cell; r is worked out
	// from it when the cell holds no other character.
	mask   uint8
	weight uint8
	// wide is set on the cell after a wide character, which it covers.
	wide bool
}

type canvas struct {
	w, h  int
	cells []cell
}

func newCanvas(w, h int) (*canvas, error) {
	if w <= 0 || h <= 0 || int64(w)*int64(h) > MaxCells {
		return nil, ErrTooLarge
	}
	return &canvas{w: w, h: h, cells: make([]cell, w*h)}, nil
}

func (c *canvas) at(x, y int) *cell {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return nil
	}
	return &c.cells[y*c.w+x]
}

// stroke adds line strokes to a cell, unless it holds a character already.
func (c *canvas) stroke(x, y int, mask uint8, weight uint8, role uint8) {
	p := c.at(x, y)
	if p == nil || p.r != 0 {
		return
	}
	p.mask |= mask
	if weight > p.weight {
		p.weight = weight
	}
	if p.role == roleNone || role == roleBox {
		p.role = role
	}
}

// put writes a character, over any strokes. A character that could steer
// the terminal or reorder the text is written as U+FFFD: see safeRune.
func (c *canvas) put(x, y int, r rune, role uint8) {
	p := c.at(x, y)
	if p == nil {
		return
	}
	p.r = safeRune(r)
	p.role = role
	p.mask = 0
	p.wide = false
}

// clear empties a cell, strokes included.
func (c *canvas) clear(x, y int) {
	if p := c.at(x, y); p != nil {
		*p = cell{}
	}
}

// text writes a string from x, wide characters taking two cells.
func (c *canvas) text(x, y int, s string, role uint8) {
	for _, r := range s {
		c.put(x, y, r, role)
		if runeWidth(r) == 2 {
			if p := c.at(x+1, y); p != nil {
				*p = cell{wide: true, role: role}
			}
			x += 2
		} else {
			x++
		}
	}
}

// hline and vline draw straight lines between two cells, ends included.
func (c *canvas) hline(x0, x1, y int, weight uint8, role uint8) {
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	for x := x0; x <= x1; x++ {
		var m uint8
		if x > x0 {
			m |= left
		}
		if x < x1 {
			m |= right
		}
		c.stroke(x, y, m, weight, role)
	}
}

func (c *canvas) vline(x, y0, y1 int, weight uint8, role uint8) {
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	for y := y0; y <= y1; y++ {
		var m uint8
		if y > y0 {
			m |= up
		}
		if y < y1 {
			m |= down
		}
		c.stroke(x, y, m, weight, role)
	}
}

// joins maps a stroke mask to its character, for the light weight.
var joins = [16]rune{
	0:                        ' ',
	up:                       '│',
	down:                     '│',
	up | down:                '│',
	left:                     '─',
	right:                    '─',
	left | right:             '─',
	down | right:             '┌',
	down | left:              '┐',
	up | right:               '└',
	up | left:                '┘',
	up | down | right:        '├',
	up | down | left:         '┤',
	left | right | down:      '┬',
	left | right | up:        '┴',
	up | down | left | right: '┼',
}

var heavyJoins = [16]rune{
	0:                        ' ',
	up:                       '┃',
	down:                     '┃',
	up | down:                '┃',
	left:                     '━',
	right:                    '━',
	left | right:             '━',
	down | right:             '┏',
	down | left:              '┓',
	up | right:               '┗',
	up | left:                '┛',
	up | down | right:        '┣',
	up | down | left:         '┫',
	left | right | down:      '┳',
	left | right | up:        '┻',
	up | down | left | right: '╋',
}

func (p *cell) glyph() rune {
	if p.r != 0 {
		return p.r
	}
	if p.mask == 0 {
		return ' '
	}
	switch p.weight {
	case heavy:
		return heavyJoins[p.mask]
	case dotted:
		switch p.mask {
		case up, down, up | down:
			return '┆'
		case left, right, left | right:
			return '┄'
		}
	}
	return joins[p.mask]
}

// plain reports whether a row holds nothing but straight vertical lines.
func (c *canvas) plain(y int) bool {
	for x := 0; x < c.w; x++ {
		p := &c.cells[y*c.w+x]
		if p.r != 0 || p.wide || (p.mask != 0 && p.mask&(left|right) != 0) {
			return false
		}
	}
	return true
}

// lines writes the canvas, without the spaces at the end of each line. Where
// rows in a row hold only vertical lines, one of them is kept: the layout's
// gaps are wider than text needs.
func (c *canvas) lines(color bool) []string {
	out := make([]string, 0, c.h)
	for y := 0; y < c.h; y++ {
		if y > 0 && c.plain(y) && c.plain(y-1) {
			continue
		}
		var b strings.Builder
		role := uint8(255)
		for x := 0; x < c.w; x++ {
			p := &c.cells[y*c.w+x]
			if p.wide {
				continue
			}
			if color && p.role != role {
				if role != 255 && roleSGR[role] != "" {
					b.WriteString("\x1b[m")
				}
				if roleSGR[p.role] != "" {
					b.WriteString("\x1b[" + roleSGR[p.role] + "m")
				}
				role = p.role
			}
			b.WriteRune(p.glyph())
		}
		if color && role != 255 && roleSGR[role] != "" {
			b.WriteString("\x1b[m")
		}
		line := b.String()
		if !color {
			line = strings.TrimRight(line, " ")
		}
		out = append(out, line)
	}
	// Drop blank lines at the start and the end.
	for len(out) > 0 && strings.TrimSpace(stripSGR(out[0])) == "" {
		out = out[1:]
	}
	for len(out) > 0 && strings.TrimSpace(stripSGR(out[len(out)-1])) == "" {
		out = out[:len(out)-1]
	}
	return out
}

func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// runeWidth is the cells a character takes: two for the wide East Asian
// ranges that pele's width table counts as wide, one otherwise.
func runeWidth(c rune) int {
	if (c >= 0x1100 && c <= 0x115f) || (c >= 0x2e80 && c <= 0xa4cf) || (c >= 0xac00 && c <= 0xd7a3) ||
		(c >= 0xf900 && c <= 0xfaff) || (c >= 0xfe30 && c <= 0xfe4f) || (c >= 0xff00 && c <= 0xff60) ||
		(c >= 0xffe0 && c <= 0xffe6) || c >= 0x1f300 {
		return 2
	}
	return 1
}

// safeRune maps the characters a terminal acts on to U+FFFD: C0 controls,
// DEL, C1 controls, the bidirectional overrides and isolates (U+202A to
// U+202E, U+2066 to U+2069), the bidi marks (U+200E, U+200F, U+061C) and the
// line and paragraph separators (U+2028, U+2029). Labels are entity-decoded, so "#27;" in a source
// is an ESC; without this, a label could set the window title or write the
// clipboard on the terminal that shows the drawing. Each of these is one cell
// wide in runeWidth, as U+FFFD is, so the layout does not move.
func safeRune(r rune) rune {
	switch {
	case r < 0x20, r >= 0x7f && r <= 0x9f,
		r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069,
		r == 0x200e, r == 0x200f, r == 0x061c, r == 0x2028, r == 0x2029:
		return '\uFFFD'
	}
	return r
}

// width is the cells a string takes.
func width(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

func widest(lines []string) int {
	w := 0
	for _, l := range lines {
		w = max(w, width(l))
	}
	return w
}
