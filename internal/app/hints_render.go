package app

import (
	"image"
	"image/color"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	uv "github.com/charmbracelet/ultraviolet"
)

func (m *OS) hintsInk() color.Color {
	if theme.Current() == nil && m.host.fg != nil {
		return m.host.fg
	}
	return theme.TerminalFg()
}

func (m *OS) applyHints(canvas *frameCanvas, id string, grounds *frameGrounds) {
	h := m.hints
	if h == nil {
		return
	}
	var p *hintsPane
	for _, q := range h.panes {
		if q.windowID == id {
			p = q
			break
		}
	}
	if p == nil {
		return
	}
	if m.hintsFocused() == nil {
		m.CloseHints()
		return
	}
	window := m.hintsPaneWindow(p)

	pal := theme.UI()
	paneBg := grounds[surfacePane].bg
	ground := paneBg
	if isNilColor(ground) {
		ground = m.terminalBg()
	}
	ink := m.hintsInk()
	faintOnly := theme.Depth() == overlay.Depth16

	labelBg, labelFg := pal.Accent, pal.PillFg
	matchFg := overlay.Readable(pal.Warning, ground)
	typedFg := overlay.Readable(pal.Accent, ground)

	rect := paneContentRect(window)
	area := canvas.Bounds()
	if v := m.sessionView; v.on {
		rect = rect.Add(image.Pt(v.dx, v.dy))
		area = area.Intersect(v.clip)
	}
	for y := range p.h {
		cy := rect.Min.Y + y
		if cy < area.Min.Y || cy >= area.Max.Y || cy >= len(canvas.Lines) {
			continue
		}
		line := canvas.Lines[cy]
		labelled := false
		for x := range p.w {
			cx := rect.Min.X + x
			if cx < area.Min.X || cx >= area.Max.X || cx >= len(line) {
				continue
			}
			cell := &line[cx]
			*cell = p.cells[y][x]
			if isNilColor(cell.Style.Bg) {
				cell.Style.Bg = paneBg
			}

			idx := y*p.w + x
			if r, ok := p.labelRune[idx]; ok {
				label := h.matches[p.labelOf[idx]].label
				if len(h.typed) == 0 || strings.HasPrefix(label, h.typed) {
					pos := x - h.matches[p.labelOf[idx]].labelAt.x
					cell.Content = string(r)
					cell.Width = 1
					cell.Style = uv.Style{Fg: labelFg, Bg: labelBg, Attrs: uv.AttrBold}
					if pos < len(h.typed) {
						cell.Style = uv.Style{Fg: typedFg, Bg: paneBg, Attrs: uv.AttrBold}
					}
					labelled = true
					continue
				}
			}
			if cell.Content == "" && cell.Width == 0 {
				continue
			}
			if owner := p.owner[idx]; owner >= 0 && (h.typed == "" || strings.HasPrefix(h.matches[owner].label, h.typed)) {
				cell.Style.Fg = matchFg
				cell.Style.Attrs |= uv.AttrBold
				cell.Style.Attrs &^= uv.AttrFaint
				continue
			}
			if faintOnly {
				if !faintIsInvisible(cell) {
					cell.Style.Attrs |= uv.AttrFaint
				}
				continue
			}
			cell.Style.Fg = h.dimFg(cell.Style.Fg, cell.Style.Bg, ink, ground)
		}
		if labelled {
			fixHintsWideCells(line, rect.Min.X, rect.Min.X+p.w)
		}
	}
}

func (h *hintsState) dimFg(fg, bg, ink, ground color.Color) color.Color {
	from := fg
	if isNilColor(from) {
		from = ink
	}
	to := bg
	if isNilColor(to) {
		to = ground
	}
	key := [2]uint32{packColor8(from), packColor8(to)}
	if c, ok := h.dimmed[key]; ok {
		return c
	}
	if h.dimmed == nil {
		h.dimmed = make(map[[2]uint32]color.Color)
	}
	c := overlay.MixColors(from, to, float64(h.dim)/100)
	h.dimmed[key] = c
	return c
}

func fixHintsWideCells(line uv.Line, from, to int) {
	to = min(to, len(line))
	for x := max(from, 0); x < to; x++ {
		c := &line[x]
		switch {
		case c.Width > 1:
			whole := x+c.Width <= to
			for k := 1; whole && k < c.Width; k++ {
				if t := line[x+k]; t.Content != "" || t.Width != 0 {
					whole = false
				}
			}
			if !whole {
				c.Content, c.Width = " ", 1
			}
		case c.Content == "" && c.Width == 0:
			owned := false
			for k := 1; k <= 3 && x-k >= from; k++ {
				p := line[x-k]
				if p.Width > 1 && p.Width > k {
					owned = true
					break
				}
				if p.Content != "" || p.Width != 0 {
					break
				}
			}
			if !owned {
				c.Content, c.Width = " ", 1
			}
		}
	}
}
