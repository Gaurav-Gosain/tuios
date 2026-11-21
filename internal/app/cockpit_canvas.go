package app

import (
	"image/color"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

type ckCanvas struct {
	w, h int
	buf  uv.ScreenBuffer
	tmp  uv.ScreenBuffer
}

type ckSeg struct {
	t      string
	fg, bg color.Color
	attrs  uint8
	link   string
}

const (
	ckBold   = uv.AttrBold
	ckItalic = uv.AttrItalic
	ckStrike = uv.AttrStrikethrough
)

func (c *ckCanvas) reset(w, h int, bg color.Color) *ckCanvas {
	if c == nil || c.w != w || c.h != h {
		c = &ckCanvas{w: w, h: h, buf: uv.NewScreenBuffer(w, h), tmp: uv.NewScreenBuffer(w, 1)}
		c.buf.Method = ansi.GraphemeWidth
		c.tmp.Method = ansi.GraphemeWidth
	}
	c.fill(0, 0, w, h, bg)
	return c
}

var ckASCII = func() (t [128]string) {
	for i := 32; i < 127; i++ {
		t[i] = string(rune(i))
	}
	return t
}()

func ckColor(c color.Color) color.Color {
	if noColor(c) {
		return nil
	}
	return c
}

func (c *ckCanvas) fill(x, y, w, h int, bg color.Color) {
	cell := uv.Cell{Content: " ", Width: 1, Style: uv.Style{Bg: ckColor(bg)}}
	for j := max(y, 0); j < min(y+h, c.h); j++ {
		line := c.buf.Lines[j]
		for i := max(x, 0); i < min(x+w, c.w); i++ {
			line[i] = cell
		}
	}
}

func (c *ckCanvas) text(x, y, limit int, segs ...ckSeg) int {
	if limit <= 0 || limit > c.w {
		limit = c.w
	}
	if y < 0 || y >= c.h {
		return x
	}
	for _, s := range segs {
		fg, bg := ckColor(s.fg), ckColor(s.bg)
		for _, r := range s.t {
			var cl string
			w := 1
			switch {
			case r < 0x20 || r == 0x7f || r >= 0x80 && r < 0xa0:
				continue
			case r < 0x80:
				cl = ckASCII[r]
			default:
				cl = string(r)
				w = ansi.StringWidth(cl)
			}
			if w == 0 {
				if prev := c.buf.CellAt(x-1, y); prev != nil && x > 0 && x <= limit {
					prev.Content += cl
				}
				continue
			}
			if x+w > limit {
				return x
			}
			if x >= 0 {
				st := uv.Style{Fg: fg, Bg: bg, Attrs: s.attrs}
				if st.Bg == nil {
					if under := c.buf.CellAt(x, y); under != nil {
						st.Bg = under.Style.Bg
					}
				}
				var link uv.Link
				if s.link != "" {
					st.Underline = uv.UnderlineSingle
					link.URL = s.link
				}
				line := c.buf.Lines[y]
				line[x] = uv.Cell{Content: cl, Width: w, Style: st, Link: link}
				for k := 1; k < w && x+k < c.w; k++ {
					line[x+k] = uv.Cell{}
				}
			}
			x += w
		}
	}
	return x
}

func (c *ckCanvas) textR(right, y int, segs ...ckSeg) int {
	x := right - ckWidth(segs...)
	c.text(x, y, right, segs...)
	return x
}

func (c *ckCanvas) draw(x, y, w int, s string) {
	if y < 0 || y >= c.h || w <= 0 {
		return
	}
	w = min(w, c.w)
	tmp := c.tmp
	area := uv.Rect(0, 0, w, 1)
	tmp.ClearArea(area)
	uv.NewStyledString(s).Draw(tmp, area)
	for i := 0; i < w && x+i < c.w; i++ {
		src := tmp.CellAt(i, 0)
		if src == nil || x+i < 0 {
			continue
		}
		cp := *src
		if cp.Content == "" && cp.Width == 0 {
			if i == 0 || x+i-1 < 0 {
				blank := uv.EmptyCell
				blank.Style = cp.Style
				c.buf.SetCell(x+i, y, &blank)
			}
			continue
		}
		if cp.Style.Bg == nil {
			if under := c.buf.CellAt(x+i, y); under != nil {
				cp.Style.Bg = under.Style.Bg
			}
		}
		c.buf.SetCell(x+i, y, &cp)
	}
}

func (c *ckCanvas) hline(x, y, w int, glyph string, fg color.Color) {
	if w > 0 {
		c.text(x, y, x+w, ckSeg{t: strings.Repeat(glyph, w), fg: fg})
	}
}

func (c *ckCanvas) mark(pal overlay.Palette, x, y, w, h int, st overlay.RowState) {
	if fn := pal.MarkCell(st); fn != nil {
		c.restyle(x, y, w, h, fn)
	}
}

func (c *ckCanvas) restyle(x, y, w, h int, fn func(*uv.Cell)) {
	for j := max(y, 0); j < min(y+h, c.h); j++ {
		for i := max(x, 0); i < min(x+w, c.w); i++ {
			if cell := c.buf.CellAt(i, j); cell != nil && cell.Width > 0 {
				fn(cell)
			}
		}
	}
}

func (c *ckCanvas) render() string {
	var b strings.Builder
	b.Grow(c.w * c.h * 4)
	for y := 0; y < c.h; y++ {
		if y > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(c.buf.Line(y).Render())
	}
	return b.String()
}

func ckWidth(segs ...ckSeg) int {
	n := 0
	for _, s := range segs {
		n += ansi.StringWidth(s.t)
	}
	return n
}

func ckTrunc(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= n {
		return s
	}
	return ansi.Truncate(s, n, overlay.Ellipsis())
}

func ckPad(s string, n int) string {
	s = ckTrunc(s, n)
	if w := ansi.StringWidth(s); w < n {
		s += strings.Repeat(" ", n-w)
	}
	return s
}
