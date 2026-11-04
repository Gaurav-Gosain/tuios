package main

import (
	"hash/maphash"
	"math"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"tuios.dev/vt"
)

// Panes are drawn from the model's own emulators, through Painter.Glyphs.
// Each distinct cell text is shaped once per font variant and kept. Each row
// keeps its background spans, its glyph runs (one per colour) and its lines,
// and is built again only when the hash of its cells changes. Box drawing and
// block elements are drawn as shapes, so lines join across cells whatever the
// font does.

// metrics is the cell size, in device pixels, at one scale.
type metrics struct {
	scale                  float32
	cellW, cellH, baseline int
}

// shaper keeps shaped cell text for every pane.
type shaper struct {
	size, lineHeight float32
	fonts            [4]ui.Font
	m                metrics
	ascii            [4][128][]ui.Glyph
	glyphs           map[glyphKey][]ui.Glyph
}

type glyphKey struct {
	s    string
	v    uint8
	wide bool
}

func newShaper(size, lineHeight float32) *shaper {
	s := &shaper{size: size, lineHeight: lineHeight, glyphs: map[glyphKey][]ui.Glyph{}}
	for i := range s.fonts {
		w := 400
		if i&1 != 0 {
			w = 700
		}
		s.fonts[i] = ui.Font{Family: monoFamily, Size: size, Weight: w, Italic: i&2 != 0}
	}
	return s
}

// at sets the scale and reports the metrics. A new scale drops every shape.
func (s *shaper) at(scale float32) metrics {
	if scale == s.m.scale {
		return s.m
	}
	fm := s.fonts[0].Metrics()
	adv := float32(0)
	for _, g := range ui.Shape("MWmw0@#", s.fonts[0]) {
		adv = max(adv, g.Advance)
	}
	m := metrics{scale: scale}
	m.cellW = max(int(math.Round(float64(adv*scale))), 1)
	natural := (fm.Ascent + fm.Descent) * scale
	m.cellH = max(int(math.Round(float64(max(natural, s.size*s.lineHeight*scale)))), 1)
	m.baseline = int(math.Round(float64((float32(m.cellH)-(fm.Ascent+fm.Descent)*scale)/2 + fm.Ascent*scale)))
	s.m = m
	s.ascii = [4][128][]ui.Glyph{}
	clear(s.glyphs)
	return m
}

// shape returns a cell text's glyphs, centred in its cell or cells.
func (s *shaper) shape(text string, v uint8, wide bool) []ui.Glyph {
	ascii := len(text) == 1 && text[0] < 128 && !wide
	if ascii {
		if g := s.ascii[v][text[0]]; g != nil {
			return g
		}
	} else if g, ok := s.glyphs[glyphKey{text, v, wide}]; ok {
		return g
	}
	shaped := ui.Shape(text, s.fonts[v])
	room := float32(s.m.cellW) / s.m.scale
	if wide {
		room *= 2
	}
	x0, x1 := float32(0), float32(0)
	for i, g := range shaped {
		if i == 0 || g.X < x0 {
			x0 = g.X
		}
		x1 = max(x1, g.X+g.Advance)
	}
	off := (room - (x1 - x0)) / 2
	if math.Abs(float64(off)) < 0.5/float64(s.m.scale) {
		off = 0
	}
	out := make([]ui.Glyph, len(shaped))
	for i, g := range shaped {
		g.X = off + g.X - x0
		out[i] = g
	}
	if ascii {
		s.ascii[v][text[0]] = out
	} else {
		if len(s.glyphs) > 8192 {
			clear(s.glyphs)
		}
		s.glyphs[glyphKey{text, v, wide}] = out
	}
	return out
}

// paneView is what one pane keeps between frames.
type paneView struct {
	lines []rowCache
	seed  maphash.Seed
	// key is what the rows were built for; a change rebuilds them all.
	key  viewKey
	used bool
}

type viewKey struct {
	scale  float32
	fg, bg ui.Color
}

type rowCache struct {
	hash  uint64
	ok    bool
	bgs   []span
	runs  []run
	decos []span
	boxes []boxCell
}

type span struct {
	x0, x1 int
	c      ui.Color
}

type run struct {
	c      ui.Color
	glyphs []ui.Glyph
}

type boxCell struct {
	x int
	r rune
	c ui.Color
}

func variantOf(attrs uint8) uint8 {
	var v uint8
	if attrs&uv.AttrBold != 0 {
		v |= 1
	}
	if attrs&uv.AttrItalic != 0 {
		v |= 2
	}
	return v
}

func pack(c ui.Color) uint64 {
	return uint64(c.R)<<24 | uint64(c.G)<<16 | uint64(c.B)<<8 | uint64(c.A)
}

// paintPane draws a pane's emulator into r. focused draws the block cursor
// filled; preedit is text an input method is composing at the cursor.
// It returns the cursor's rectangle, for the input method's window.
func (a *guiApp) paintPane(p *ui.Painter, r ui.Rect, pn paneSnap, focused bool, preedit string) ui.Rect {
	sh := a.shaper
	m := sh.at(p.Scale())
	s := m.scale
	pv := a.views[pn.ID]
	if pv == nil {
		pv = &paneView{seed: maphash.MakeSeed()}
		a.views[pn.ID] = pv
	}
	pv.used = true
	defFg, defBg := a.roles.fg, a.roles.bg
	if k := (viewKey{s, defFg, defBg}); k != pv.key {
		pv.key = k
		pv.lines = pv.lines[:0]
	}

	w := pn.win
	w.RLockIO()
	t := w.Terminal
	if t == nil {
		w.RUnlockIO()
		return ui.Rect{}
	}
	tw, th := t.Width(), t.Height()
	if len(pv.lines) != th {
		pv.lines = make([]rowCache, th)
	}
	for y := range th {
		a.buildRow(pv, t, y, tw, m)
	}
	cur := t.CursorPosition()
	hidden := t.IsCursorHidden()
	var curCell uv.Cell
	if c := t.CellAt(cur.X, cur.Y); c != nil {
		curCell = *c
	}
	w.RUnlockIO()

	cw, ch := float32(m.cellW)/s, float32(m.cellH)/s
	// Whole device pixels for the grid's origin, so glyphs land on the
	// pixel grid.
	ox := float32(math.Round(float64(r.X*s))) / s
	oy := float32(math.Round(float64(r.Y*s))) / s
	base := float32(m.baseline) / s
	for y := range pv.lines {
		ry := oy + float32(y)*ch
		for _, b := range pv.lines[y].bgs {
			p.Fill(ui.Rect{X: ox + float32(b.x0)*cw, Y: ry, W: float32(b.x1-b.x0) * cw, H: ch}, b.c, 0)
		}
	}
	curRect := ui.Rect{X: ox + float32(cur.X)*cw, Y: oy + float32(cur.Y)*ch, W: cw, H: ch}
	if curCell.Width == 2 {
		curRect.W *= 2
	}
	showCursor := !hidden && cur.Y < len(pv.lines) && preedit == ""
	if showCursor {
		if focused {
			p.Fill(curRect, a.roles.cur, 0)
		} else {
			p.Stroke(curRect, a.roles.cur, 0, 1/s)
		}
	}
	thick := max(float32(math.Round(float64(sh.size*s/14))), 1) / s
	ul := base + max(float32(math.Round(float64(sh.size*s/10))), 1)/s
	for y := range pv.lines {
		line := &pv.lines[y]
		ry := oy + float32(y)*ch
		for _, rn := range line.runs {
			p.Glyphs(rn.glyphs, ox, ry+base, rn.c)
		}
		for _, d := range line.decos {
			p.Fill(ui.Rect{X: ox + float32(d.x0)*cw, Y: ry + ul, W: float32(d.x1-d.x0) * cw, H: thick}, d.c, 0)
		}
		for _, b := range line.boxes {
			drawBox(p, b.r, ui.Rect{X: ox + float32(b.x)*cw, Y: ry, W: cw, H: ch}, b.c, s)
		}
	}
	if showCursor && focused && curCell.Content != "" && curCell.Content != " " {
		gl := sh.shape(curCell.Content, variantOf(curCell.Style.Attrs), curCell.Width == 2)
		p.Glyphs(gl, curRect.X, curRect.Y+base, defBg)
	}
	if preedit != "" {
		// The text the input method composes, at the cursor, underlined.
		var x float32
		for _, cl := range splitClusters(preedit) {
			wide := runeWide(cl)
			n := float32(1)
			if wide {
				n = 2
			}
			cell := ui.Rect{X: curRect.X + x, Y: curRect.Y, W: cw * n, H: ch}
			p.Fill(cell, defBg, 0)
			p.Glyphs(sh.shape(cl, 0, wide), cell.X, cell.Y+base, defFg)
			x += cw * n
		}
		p.Fill(ui.Rect{X: curRect.X, Y: curRect.Y + ul, W: x, H: thick}, defFg, 0)
	}
	return curRect
}

// buildRow builds row y again when its cells changed since the last frame.
func (a *guiApp) buildRow(pv *paneView, t vt.Terminal, y, width int, m metrics) {
	line := &pv.lines[y]
	defFg, defBg := a.roles.fg, a.roles.bg
	var h maphash.Hash
	h.SetSeed(pv.seed)
	var word [8]byte
	put := func(v uint64) {
		for i := range 8 {
			word[i] = byte(v >> (8 * i))
		}
		h.Write(word[:])
	}
	for x := range width {
		c := t.CellAt(x, y)
		if c == nil {
			put(0)
			continue
		}
		h.WriteString(c.Content)
		fg, bg := a.roles.cellColor(c.Style.Fg, defFg), a.roles.cellColor(c.Style.Bg, defBg)
		put(pack(fg)<<32 | pack(bg))
		put(uint64(c.Width)<<16 | uint64(c.Style.Attrs)<<8 | uint64(c.Style.Underline))
	}
	sum := h.Sum64()
	if line.ok && line.hash == sum {
		return
	}
	line.hash, line.ok = sum, true
	line.bgs, line.decos, line.boxes = line.bgs[:0], line.decos[:0], line.boxes[:0]
	for i := range line.runs {
		line.runs[i].glyphs = line.runs[i].glyphs[:0]
	}
	nruns := 0
	cw := float32(m.cellW) / m.scale
	for x := 0; x < width; x++ {
		c := t.CellAt(x, y)
		if c == nil || c.Width == 0 {
			continue
		}
		fg, bg := a.roles.cellColor(c.Style.Fg, defFg), a.roles.cellColor(c.Style.Bg, defBg)
		if c.Style.Attrs&uv.AttrReverse != 0 {
			fg, bg = bg, fg
		}
		if c.Style.Attrs&uv.AttrFaint != 0 {
			fg = fg.Alpha(0.5)
		}
		w := max(c.Width, 1)
		if bg != defBg {
			if n := len(line.bgs); n > 0 && line.bgs[n-1].x1 == x && line.bgs[n-1].c == bg {
				line.bgs[n-1].x1 = x + w
			} else {
				line.bgs = append(line.bgs, span{x, x + w, bg})
			}
		}
		if c.Style.Underline != 0 {
			uc := fg
			if c.Style.UnderlineColor != nil {
				uc = a.roles.cellColor(c.Style.UnderlineColor, fg)
			}
			if n := len(line.decos); n > 0 && line.decos[n-1].x1 == x && line.decos[n-1].c == uc {
				line.decos[n-1].x1 = x + w
			} else {
				line.decos = append(line.decos, span{x, x + w, uc})
			}
		}
		if c.Content == "" || c.Content == " " || c.Style.Attrs&uv.AttrConceal != 0 {
			continue
		}
		if rs := []rune(c.Content); len(rs) == 1 && isBox(rs[0]) {
			line.boxes = append(line.boxes, boxCell{x, rs[0], fg})
			continue
		}
		gl := a.shaper.shape(c.Content, variantOf(c.Style.Attrs), w == 2)
		ri := -1
		for i := range nruns {
			if line.runs[i].c == fg {
				ri = i
				break
			}
		}
		if ri < 0 {
			if nruns == len(line.runs) {
				line.runs = append(line.runs, run{})
			}
			ri = nruns
			nruns++
			line.runs[ri].c = fg
		}
		cellX := float32(x) * cw
		for _, one := range gl {
			one.X += cellX
			line.runs[ri].glyphs = append(line.runs[ri].glyphs, one)
		}
	}
	line.runs = line.runs[:nruns]
	if nruns > 0 && a.first.Load() == 0 {
		a.first.CompareAndSwap(0, nowNano())
	}
}

// dropUnused forgets the views of panes no longer drawn.
func (a *guiApp) dropUnused() {
	for id, v := range a.views {
		if !v.used {
			delete(a.views, id)
		}
		v.used = false
	}
}

// paneText is a pane's screen as text, for the control socket.
func paneText(w *terminal.Window) (cols, rows int, text []string) {
	w.RLockIO()
	defer w.RUnlockIO()
	t := w.Terminal
	if t == nil {
		return 0, 0, nil
	}
	cols, rows = t.Width(), t.Height()
	text = make([]string, rows)
	for y := range rows {
		b := make([]byte, 0, cols)
		for x := range cols {
			c := t.CellAt(x, y)
			switch {
			case c != nil && c.Content != "":
				b = append(b, c.Content...)
			case c == nil || c.Width != 0:
				b = append(b, ' ')
			}
		}
		for len(b) > 0 && b[len(b)-1] == ' ' {
			b = b[:len(b)-1]
		}
		text[y] = string(b)
	}
	return cols, rows, text
}
