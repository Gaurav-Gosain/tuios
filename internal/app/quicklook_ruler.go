package app

// The ruler on the right edge of quick look's text and Markdown: the thread's
// ruler (ckRuler in cockpit_follow.go) with its track drawn, so the mouse can
// take it. A click on the track takes the thumb there, and a press on the
// thumb drags it. A Markdown file's headings are marks on it, the way the
// agents thread marks its prompts.
//
// A text read in windows has no line count. Its ruler counts lines of the
// average length of the lines held, so the thumb says where the held text is
// in the file, and a move past the held text reads the window at that byte.

// quickLookRuler is the ruler in the last frame: what it showed, and where it
// was in the body. perLine is the bytes a line counts for in a text read in
// windows, 0 when the ruler counts real lines, and base the line the held
// text starts at then.
type quickLookRuler struct {
	ok           bool
	r            ckRuler
	x, top, rows int
	perLine      int64
	base         int
}

// quickLookTextRuler is the ruler of a text shown from held line from.
func quickLookTextRuler(r *quickLookResult, from int) quickLookRuler {
	n := len(r.lines)
	if n == 0 || r.size <= 0 {
		return quickLookRuler{}
	}
	if !r.more && r.startOff == 0 {
		return quickLookRuler{r: ckRuler{lines: n, start: from, track: true}}
	}
	per := max((r.endOff-r.startOff)/int64(n), 1)
	base := int(r.startOff / per)
	lines := int(max(r.size/per, int64(base+n)))
	return quickLookRuler{r: ckRuler{lines: lines, start: base + from, track: true}, perLine: per, base: base}
}

// drawQuickLookRuler draws g in column x of the body, rows top to top+rows,
// and keeps it for the mouse.
func (m *OS) drawQuickLookRuler(c *ckCanvas, l ckLook, g quickLookRuler, x, top, rows int) {
	g.x, g.top, g.rows = x, top, rows
	g.ok = g.r.draw(c, l, x, top, rows)
	m.quickLook.ruler = g
}

// quickLookRulerClick takes a press on the ruler. x and y are on the screen.
// A press on the track takes the thumb there, by its middle, and a press on
// the thumb takes it where it is. A drag goes on from either. It reports
// whether the press was on the ruler.
func (m *OS) quickLookRulerClick(x, y int) bool {
	ql := &m.quickLook
	g := ql.ruler
	x, y = x-ql.box.X0-1, y-ql.box.Y0-1
	if !g.ok || ql.copy != nil || x != g.x || y < g.top || y >= g.top+g.rows {
		return false
	}
	row := y - g.top
	top, size := g.r.thumb(g.rows)
	ql.rulerGrab = row - top
	if row < top || row >= top+size {
		ql.rulerGrab = size / 2
		m.quickLookRulerTo(row - ql.rulerGrab)
	}
	ql.rulerDrag = true
	return true
}

// quickLookRulerTo moves the view so the thumb's first row is at row top of
// the ruler.
func (m *OS) quickLookRulerTo(top int) {
	ql := &m.quickLook
	g := ql.ruler
	start := g.r.startFor(top, g.rows)
	r := ql.res
	if g.perLine == 0 || !quickLookText(r) {
		ql.scroll = start
		return
	}
	switch i := start - g.base; {
	case i >= 0 && i < len(r.lines):
		ql.scroll, ql.jump = i, quickLookNoMove
	case start <= 0:
		m.QuickLookEdge(false)
	case start >= g.r.lines-g.rows:
		m.QuickLookEdge(true)
	default:
		// Past the held text: the window at that byte replaces it.
		ql.atOff, ql.jump = int64(start)*g.perLine, quickLookAt
	}
}

// quickLookRulerDrag follows the pointer at screen row y while a press on
// the ruler is held. It reports whether one is.
func (m *OS) quickLookRulerDrag(y int) bool {
	ql := &m.quickLook
	if !ql.open || !ql.rulerDrag || !ql.ruler.ok {
		return false
	}
	m.quickLookRulerTo(y - ql.box.Y0 - 1 - ql.ruler.top - ql.rulerGrab)
	return true
}
