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

func rect(x, y, w, h int) Rect { return image.Rect(x, y, x+max(w, 0), y+max(h, 0)) }

const (
	attrBold      = uv.AttrBold
	attrFaint     = uv.AttrFaint
	attrItalic    = uv.AttrItalic
	attrReverse   = uv.AttrReverse
	attrStrike    = uv.AttrStrikethrough
	attrUnderline = uv.AttrRapidBlink
)

type style struct {
	fg, bg string
	attrs  uint8
	link   string
}

func (s style) with(bits uint8) style { s.attrs |= bits; return s }

type canvas struct {
	buf   *uv.Buffer
	clip  Rect
	pal   *Palette
	g     *glyphs
	cells *int
	links map[image.Point]string
	osc8  bool
}

func (c *canvas) withClip(r Rect) *canvas {
	cp := *c
	cp.clip = c.clip.Intersect(r)
	return &cp
}

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
			for k := range w {
				c.links[image.Point{x + k, y}] = st.link
			}
		} else {
			for k := range w {
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
	for k := 1; k < w; k++ {
		if under := line.At(x + k); under != nil && under.Width != 1 {
			line.Set(x+k, nil)
		}
	}
	line.Set(x, &cell)
}

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

func (c *canvas) textRight(right, y int, s string, st style) int {
	x := right - width(s)
	c.text(x, y, s, st, -1)
	return x
}

func (c *canvas) hline(x, y, n int, g string, st style) {
	for i := range n {
		c.put(x+i, y, g, 1, st)
	}
}

func nextGrapheme(s *string) (string, int) {
	if t := *s; t != "" && t[0] >= 0x20 && t[0] < 0x7f && (len(t) == 1 || t[1] < 0x80) {
		*s = t[1:]
		return t[:1], 1
	}
	g, w := ansi.FirstGraphemeCluster(*s, ansi.GraphemeWidth)
	if g == "" {
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

func width(s string) int {
	n := 0
	for s != "" {
		_, w := nextGrapheme(&s)
		n += w
	}
	return n
}

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
