package app

import (
	"image/color"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	uv "github.com/charmbracelet/ultraviolet"
)

// The hints frame is a pass over the composed canvas, the way the scrim is,
// not a layer and not a change to the pane's own render.
//
// composeLayers calls applyHints right after it draws the pane hints mode is
// open on. The pass writes the copied view over the pane's content rectangle
// (so the text holds still under the labels), dims everything that is not a
// match, lights the matches, and puts the labels on them. Anything drawn
// above the pane (a floating pane, a panel, a message) is drawn after this and
// stays on top. The pane's cached layer is never touched, so closing hints
// mode is nothing more than the next frame not running this pass.
//
// It runs only while hints mode is open. With it closed the cost is the nil
// check in composeLayers.

// hintsInk is the colour of text that names no colour of its own, which is
// what a dimmed cell is dimmed from.
func (m *OS) hintsInk() color.Color {
	if theme.Current() == nil && m.host.fg != nil {
		return m.host.fg
	}
	return theme.TerminalFg()
}

// applyHints draws hints mode over the pane whose layer id is id.
func (m *OS) applyHints(canvas *frameCanvas, id string, grounds *frameGrounds) {
	h := m.hints
	if h == nil || id != h.windowID {
		return
	}
	window := m.hintsWindow()
	if window == nil {
		// The copy no longer matches the screen. Drawing it would put labels
		// on text that has moved, so hints mode ends here.
		m.CloseHints()
		return
	}

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
	// A label whose first letters are typed shows them quieter, so the next
	// letter to press is the loud one.
	typedFg := overlay.Readable(pal.Accent, ground)

	rect := paneContentRect(window)
	area := canvas.Bounds()
	for y := range h.h {
		cy := rect.Min.Y + y
		if cy < area.Min.Y || cy >= area.Max.Y || cy >= len(canvas.Lines) {
			continue
		}
		line := canvas.Lines[cy]
		labelled := false
		for x := range h.w {
			cx := rect.Min.X + x
			if cx < area.Min.X || cx >= area.Max.X || cx >= len(line) {
				continue
			}
			cell := &line[cx]
			*cell = h.cells[y][x]
			if isNilColor(cell.Style.Bg) {
				cell.Style.Bg = paneBg
			}

			idx := y*h.w + x
			if r, ok := h.labelRune[idx]; ok {
				label := h.matches[h.labelOf[idx]].label
				if len(h.typed) == 0 || strings.HasPrefix(label, h.typed) {
					pos := x - h.matches[h.labelOf[idx]].labelAt.x
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
				// The tail of a wide glyph. Styling it would draw it as a
				// cell of its own.
				continue
			}
			if owner := h.owner[idx]; owner >= 0 && (h.typed == "" || strings.HasPrefix(h.matches[owner].label, h.typed)) {
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
			fixHintsWideCells(line, rect.Min.X, rect.Min.X+h.w)
		}
	}
}

// dimFg carries a cell's text toward its ground by the configured percent.
// A cell that names no colour is dimmed from the terminal's own text colour.
// The results are kept, because a pane has a few dozen colours and a frame
// asks about thousands of cells.
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

// fixHintsWideCells repairs the wide glyphs a label was written over. A label
// letter is one column, so a wide glyph under it loses its second half, and a
// glyph whose tail a label covered has nothing left to draw into. Either one
// left as it is would push the rest of the row a column over.
func fixHintsWideCells(line uv.Line, from, to int) {
	to = min(to, len(line))
	for x := max(from, 0); x < to; x++ {
		c := &line[x]
		switch {
		case c.Width > 1:
			// A wide glyph needs its tail cells to still be tails.
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
			// A tail must follow its glyph.
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
