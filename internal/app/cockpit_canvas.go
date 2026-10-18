package app

import (
	"image/color"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// ckCanvas is the grid the cockpit is drawn on: grounds first, then runs of
// text that keep the ground under them. The cockpit has three columns, cards
// and right-aligned meta, and laying them out by cell is simpler and safer
// than padding styled strings to widths.
type ckCanvas struct {
	w, h int
	buf  uv.ScreenBuffer
}

// ckSeg is one styled run of text. A nil ground keeps the cell's.
type ckSeg struct {
	t      string
	fg, bg color.Color
	attrs  uint8
}

// Styles of a run.
const (
	ckBold   = uv.AttrBold
	ckItalic = uv.AttrItalic
	ckStrike = uv.AttrStrikethrough
)

func newCkCanvas(w, h int, bg color.Color) *ckCanvas {
	c := &ckCanvas{w: w, h: h, buf: uv.NewScreenBuffer(w, h)}
	c.fill(0, 0, w, h, bg)
	return c
}

// ckColor drops a colour that is no colour at all, which a cell would write
// as black.
func ckColor(c color.Color) color.Color {
	if noColor(c) {
		return nil
	}
	return c
}

// fill paints a rectangle of blank cells on bg.
func (c *ckCanvas) fill(x, y, w, h int, bg color.Color) {
	cell := uv.Cell{Content: " ", Width: 1, Style: uv.Style{Bg: ckColor(bg)}}
	// The canvas is only ever rendered line by line, so its cells are
	// written directly, past the buffer's damage tracking.
	for j := max(y, 0); j < min(y+h, c.h); j++ {
		line := c.buf.Lines[j]
		for i := max(x, 0); i < min(x+w, c.w); i++ {
			line[i] = cell
		}
	}
}

// text draws segs at x, y, clipped at limit (the canvas edge when 0), and
// returns the column after the last cell drawn.
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
			cl := string(r)
			w := ansi.StringWidth(cl)
			if w == 0 {
				// A combining mark joins the cell before it.
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
				line := c.buf.Lines[y]
				line[x] = uv.Cell{Content: cl, Width: w, Style: st}
				for k := 1; k < w && x+k < c.w; k++ {
					line[x+k] = uv.Cell{}
				}
			}
			x += w
		}
	}
	return x
}

// textR draws segs so they end at column right, and returns where they
// start.
func (c *ckCanvas) textR(right, y int, segs ...ckSeg) int {
	x := right - ckWidth(segs...)
	c.text(x, y, right, segs...)
	return x
}

// draw lays a styled one-line string at x, y, at most w cells of it. Cells
// it draws with no ground keep the ground under them.
func (c *ckCanvas) draw(x, y, w int, s string) {
	if y < 0 || y >= c.h || w <= 0 {
		return
	}
	tmp := uv.NewScreenBuffer(w, 1)
	uv.NewStyledString(s).Draw(tmp, tmp.Bounds())
	for i := 0; i < w && x+i < c.w; i++ {
		src := tmp.CellAt(i, 0)
		if src == nil || x+i < 0 {
			continue
		}
		cp := *src
		if cp.Content == "" && cp.Width == 0 {
			// The second cell of a wide character.
			c.buf.SetCell(x+i, y, &cp)
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

// hline draws a run of glyph in fg.
func (c *ckCanvas) hline(x, y, w int, glyph string, fg color.Color) {
	if w > 0 {
		c.text(x, y, x+w, ckSeg{t: strings.Repeat(glyph, w), fg: fg})
	}
}

// restyle applies fn to every cell of the rectangle, for the 16-colour
// attributes that stand in for a ground.
func (c *ckCanvas) restyle(x, y, w, h int, fn func(*uv.Cell)) {
	for j := max(y, 0); j < min(y+h, c.h); j++ {
		for i := max(x, 0); i < min(x+w, c.w); i++ {
			if cell := c.buf.CellAt(i, j); cell != nil && cell.Width > 0 {
				fn(cell)
			}
		}
	}
}

// render is the canvas as lines of styled text.
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

// ckWidth is the width of segs in cells.
func ckWidth(segs ...ckSeg) int {
	n := 0
	for _, s := range segs {
		n += ansi.StringWidth(s.t)
	}
	return n
}

// ckTrunc cuts s to n cells with an ellipsis.
func ckTrunc(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= n {
		return s
	}
	return ansi.Truncate(s, n, overlay.Ellipsis())
}

// ckPad pads or cuts s to n cells.
func ckPad(s string, n int) string {
	s = ckTrunc(s, n)
	if w := ansi.StringWidth(s); w < n {
		s += strings.Repeat(" ", n-w)
	}
	return s
}
