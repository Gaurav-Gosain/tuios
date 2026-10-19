package loom

import (
	"image"
	"strings"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// Rect is a rectangle of cells.
type Rect = image.Rectangle

// rect returns the rectangle at x, y of w by h cells.
func rect(x, y, w, h int) Rect { return image.Rect(x, y, x+max(w, 0), y+max(h, 0)) }

// Attribute bits of a style, the same as ultraviolet's.
const (
	attrBold      = uv.AttrBold
	attrFaint     = uv.AttrFaint
	attrItalic    = uv.AttrItalic
	attrReverse   = uv.AttrReverse
	attrStrike    = uv.AttrStrikethrough
	attrUnderline = uv.AttrRapidBlink // loom never blinks; it carries underline, which ultraviolet keeps apart
)

// style is a text style in roles. An empty fg is the fg role. An empty bg
// keeps the ground already under the cell.
type style struct {
	fg, bg string
	attrs  uint8
	link   string
}

func (s style) with(bits uint8) style { s.attrs |= bits; return s }

// canvas draws onto an ultraviolet buffer through a clip rectangle, with
// colours from a palette.
type canvas struct {
	buf   *uv.Buffer
	clip  Rect
	pal   *Palette
	g     *glyphs
	cells *int // counts the cells written, when set
	// links records the link under each cell, for a click; osc8 also
	// writes it into the cell.
	links map[image.Point]string
	osc8  bool
}

// withClip returns a copy of c that draws only inside r and the current clip.
func (c *canvas) withClip(r Rect) *canvas {
	cp := *c
	cp.clip = c.clip.Intersect(r)
	return &cp
}

// fill paints r with blank cells on the ground role. An empty role or a
// role with no colour at this depth leaves no colour.
func (c *canvas) fill(r Rect, ground string) {
	r = r.Intersect(c.clip)
	bg := c.pal.Color(ground)
	cell := uv.Cell{Content: " ", Width: 1, Style: uv.Style{Bg: bg}}
	if c.cells != nil {
		*c.cells += r.Dx() * r.Dy()
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		line := c.buf.Line(y)
		for x := r.Min.X; x < r.Max.X; x++ {
			line.Set(x, &cell)
			if c.links != nil {
				delete(c.links, image.Point{x, y})
			}
		}
	}
}

// resolve turns a style into an ultraviolet style. A cell with no bg keeps
// the background of the cell under it.
func (c *canvas) resolve(st style, under *uv.Cell) uv.Style {
	fg := st.fg
	if fg == "" {
		fg = "fg"
	}
	out := uv.Style{Fg: c.pal.Color(fg), Attrs: st.attrs &^ attrUnderline}
	if st.attrs&attrUnderline != 0 {
		out.Underline = uv.UnderlineSingle
	}
	switch {
	case st.bg != "":
		out.Bg = c.pal.Color(st.bg)
	case under != nil:
		out.Bg = under.Style.Bg
	}
	return out
}

// put draws one grapheme of width w at x, y.
func (c *canvas) put(x, y int, g string, w int, st style) {
	if !(image.Point{x, y}.In(c.clip)) || x+w > c.clip.Max.X {
		return
	}
	line := c.buf.Line(y)
	if line == nil {
		return
	}
	cell := uv.Cell{Content: g, Width: w, Style: c.resolve(st, line.At(x))}
	if c.links != nil {
		if st.link != "" {
			for k := 0; k < w; k++ {
				c.links[image.Point{x + k, y}] = st.link
			}
		} else {
			for k := 0; k < w; k++ {
				delete(c.links, image.Point{x + k, y})
			}
		}
	}
	if st.link != "" && c.osc8 {
		cell.Link = uv.NewLink(st.link)
	}
	if c.cells != nil {
		*c.cells += w
	}
	line.Set(x, &cell)
}

// text draws s from x on row y, no further than x+limit, and returns the
// column after the last cell. A negative limit means no limit but the clip.
// Control characters are dropped and invalid UTF-8 becomes U+FFFD.
func (c *canvas) text(x, y int, s string, st style, limit int) int {
	end := c.clip.Max.X
	if limit >= 0 {
		end = min(end, x+limit)
	}
	for s != "" {
		g, w := nextGrapheme(&s)
		if w == 0 {
			continue
		}
		if x+w > end {
			break
		}
		if g == "\uFFFD" && c.g == &asciiGlyphs {
			g = "?"
		}
		if x >= c.clip.Min.X {
			c.put(x, y, g, w, st)
		}
		x += w
	}
	return x
}

// textRight draws s so that it ends at column right, exclusive, and returns
// the column it starts at.
func (c *canvas) textRight(right, y int, s string, st style) int {
	x := right - width(s)
	c.text(x, y, s, st, -1)
	return x
}

// hline draws n copies of a one-cell glyph.
func (c *canvas) hline(x, y, n int, g string, st style) {
	for i := 0; i < n; i++ {
		c.put(x+i, y, g, 1, st)
	}
}

// nextGrapheme cuts the first grapheme off *s and returns it with its width.
// A control character has width 0 and is dropped by callers. Invalid UTF-8
// becomes U+FFFD.
func nextGrapheme(s *string) (string, int) {
	g, w := ansi.FirstGraphemeCluster(*s, ansi.GraphemeWidth)
	if g == "" {
		// Defensive: never loop on input the segmenter cannot cut.
		_, size := utf8.DecodeRuneInString(*s)
		*s = (*s)[max(size, 1):]
		return "", 0
	}
	*s = (*s)[len(g):]
	r, _ := utf8.DecodeRuneInString(g)
	switch {
	case r == utf8.RuneError && !utf8.ValidString(g):
		return "�", 1
	case r < 0x20 || (r >= 0x7f && r < 0xa0):
		return "", 0
	case w > 2:
		w = 2
	}
	return g, w
}

// width returns the width of s in cells, as canvas.text counts it.
func width(s string) int {
	n := 0
	for s != "" {
		_, w := nextGrapheme(&s)
		n += w
	}
	return n
}

// truncate cuts s to at most n cells. A cut string ends in tail.
func truncate(s string, n int, tail string) string {
	if n <= 0 {
		return ""
	}
	if width(s) <= n {
		return s
	}
	tw := width(tail)
	if tw > n {
		tail, tw = "", 0
	}
	var b strings.Builder
	used := 0
	for s != "" {
		g, w := nextGrapheme(&s)
		if w == 0 {
			continue
		}
		if used+w > n-tw {
			break
		}
		b.WriteString(g)
		used += w
	}
	return b.String() + tail
}

// glyphs are the marks loom draws itself, in unicode or in ASCII.
type glyphs struct {
	ellipsis, cursor, foldOpen, foldShut                                    string
	hline, vline, heavy, tl, tr, bl, br, sortUp, sortDown, dot, tabRule     string
	ruleSoft, radioOn, radioOff, check, cross, bullet, sliderKnob, scrollTh string
	scrollTrack, left, right                                                string
}

var unicodeGlyphs = glyphs{
	ellipsis: "…", cursor: "▌", foldOpen: "▾", foldShut: "▸",
	hline: "─", vline: "│", heavy: "━", tl: "╭", tr: "╮", bl: "╰", br: "╯",
	sortUp: "▲", sortDown: "▼", dot: "●", tabRule: "━", ruleSoft: "─",
	radioOn: "●", radioOff: "○", check: "✓", cross: "✕", bullet: "•",
	sliderKnob: "●", scrollTh: "┃", scrollTrack: "│", left: "‹", right: "›",
}

var asciiGlyphs = glyphs{
	ellipsis: "~", cursor: ">", foldOpen: "v", foldShut: ">",
	hline: "-", vline: "|", heavy: "=", tl: "+", tr: "+", bl: "+", br: "+",
	sortUp: "^", sortDown: "v", dot: "o", tabRule: "=", ruleSoft: "-",
	radioOn: "(*)", radioOff: "( )", check: "v", cross: "x", bullet: "*",
	sliderKnob: "O", scrollTh: "#", scrollTrack: "|", left: "<", right: ">",
}
