package loom

// The page: a document taller than its surface scrolls as a whole.
//
// Layout first places the document in the surface, where the tables, lists
// and scroll boxes give rows back before anything else (see give). When that
// still cuts something that does not scroll by itself, such as text, a card
// or a chart's rows, the document is laid out on a taller page instead, as
// tall as it needs to cut nothing, and the surface shows one window of it
// with a scroll bar in the last column. The keys and the wheel that the
// focused box does not use move the window. The offset is view state
// (View.Page), so it stays across patches, replaces and resizes, and a
// layout clamps it when the document gets shorter.
//
// Nothing here runs on a timer. A scroll marks the surface for a new layout,
// and the next Render places and paints again without evaluating any
// binding.

// pageWidget is the root's widget: the scroll bar of a page that does not
// fit.
type pageWidget struct {
	over    bool
	content int // the page's height
}

func (w *pageWidget) measure(s *Surface, b *box, maxW int) (int, int) {
	return s.measureCol(b.kids, maxW, b.gap)
}

func (w *pageWidget) paint(s *Surface, b *box, c *canvas, r Rect) {
	if w.over {
		paintBar(s, c, r, w.content, s.view.Page)
	}
}

// paintBar draws a scroll bar in the last column of r, for content rows
// scrolled off rows down.
func paintBar(s *Surface, c *canvas, r Rect, content, off int) {
	if r.Dy() <= 0 || r.Dx() <= 0 {
		return
	}
	x := r.Max.X - 1
	thumb := max(1, r.Dy()*r.Dy()/max(content, 1))
	top := 0
	if content > r.Dy() {
		top = (r.Dy() - thumb) * off / (content - r.Dy())
	}
	// The track is a rule in mute, which reads on a dark ground where edge
	// does not, and the thumb is dim, a step brighter, so the two differ at
	// 16 colours too, where edge and mute are one slot.
	for y := 0; y < r.Dy(); y++ {
		g, st := s.g.scrollTrack, style{fg: "mute"}
		if y >= top && y < top+thumb {
			g, st = s.g.scrollTh, style{fg: "dim"}
		}
		c.text(x, r.Min.Y+y, g, st, 1)
	}
}

// pageOf returns the root's page widget, or nil for a fragment, which never
// scrolls as a page.
func (s *Surface) pageOf() *pageWidget {
	if s.root == nil {
		return nil
	}
	pw, _ := s.root.w.(*pageWidget)
	return pw
}

// layoutPage lays the document out on a taller page when the surface cuts
// something, after placeRoot placed it in the surface.
func (s *Surface) layoutPage() {
	pw := s.pageOf()
	if pw == nil {
		return
	}
	pw.over = false
	if s.w < 2 || s.h < 1 || s.cutBy(s.root) == 0 {
		return
	}
	// The scroll bar takes the last column.
	w := s.w - 1
	natural := s.pageHeight(w)
	if natural <= s.h {
		// Whole at its natural height: what cut it was a size the
		// document asked for, which a taller page does not undo.
		s.placeRoot(0, s.w, s.h)
		return
	}
	page, cut := s.h, s.cutBy(s.root)
	for range 4 {
		page = min(page+max(cut, 1), natural)
		s.placeRoot(0, w, page)
		if cut = s.cutBy(s.root); page == natural || cut == 0 {
			break
		}
	}
	if cut > 0 && page < natural {
		// At its natural height nothing is cut.
		page = natural
		s.placeRoot(0, w, page)
	}
	// On a page, a table, list or scroll box need not stay at the few rows
	// it gave back: it gets rows up to the surface's height, so it shows a
	// screen of rows when the page brings it into view.
	if short := s.scrollersShort(s.root); short > 0 && page < natural {
		page = min(page+short, natural)
		s.placeRoot(0, w, page)
	}
	pw.over, pw.content = true, page
	// The place is the box that was at the top, so a new width that
	// reflows the page, such as a zoom, keeps the same content in view.
	off := s.view.Page
	if b := s.placedBox(s.root, s.view.PageAnchor); b != nil {
		off = b.rect.Min.Y + min(s.view.PageRow, max(b.rect.Dy()-1, 0))
	}
	off = clampInt(off, 0, page-s.h)
	// After tab, or a key the focused box took, bring the focus into view:
	// the person sees what their key did.
	if s.focusMoved || s.reveal {
		if f := s.focused(); f != nil {
			r := focusRect(f)
			switch {
			case r.Min.Y < off:
				off = r.Min.Y
			case r.Max.Y > off+s.h:
				off = min(r.Max.Y-s.h, r.Min.Y)
			}
			off = clampInt(off, 0, page-s.h)
		}
	}
	s.view.Page = off
	s.view.PageAnchor, s.view.PageRow = "", 0
	if b := s.boxAt(s.root, off); b != nil {
		s.view.PageAnchor, s.view.PageRow = b.key, off-b.rect.Min.Y
	}
	if off != 0 {
		s.placeRoot(off, w, page)
	}
}

// placedBox finds the box with key that the last layout placed, inside b,
// or nil. The boxes inside a box that scrolls are left out: their rows move
// with its scroll.
func (s *Surface) placedBox(b *box, key string) *box {
	if key == "" {
		return nil
	}
	for _, k := range b.kids {
		if k.hidden || k.pgen != s.gen {
			continue
		}
		if k.key == key {
			return k
		}
		if scrolls(k) {
			continue
		}
		if f := s.placedBox(k, key); f != nil {
			return f
		}
	}
	return nil
}

// boxAt finds the innermost box inside b that the last layout placed over
// page row y, or on a gap or padding row, the first box under it. It returns
// nil when there is none. It does not go inside a box that scrolls.
func (s *Surface) boxAt(b *box, y int) *box {
	var below *box
	for _, k := range b.kids {
		if k.hidden || k.pgen != s.gen || k.rect.Empty() {
			continue
		}
		if y < k.rect.Min.Y {
			if below == nil {
				below = k
			}
			continue
		}
		if y >= k.rect.Max.Y {
			continue
		}
		if scrolls(k) {
			return k
		}
		if in := s.boxAt(k, y); in != nil {
			return in
		}
		return k
	}
	return below
}

// pageHeight is the document's natural height at width w.
func (s *Surface) pageHeight(w int) int {
	if len(s.root.kids) == 1 && s.root.kids[0].height.kind == sizeAuto {
		k := s.root.kids[0]
		_, h := s.measure(k, s.colChildWidth(s.root, k, w))
		return h
	}
	_, h := s.measureCol(s.root.kids, w, s.root.gap)
	return h
}

// cutBy returns how many rows the last layout cut from the boxes inside b
// that do not scroll by themselves. A box that scrolls, a box with a height
// of its own and what is inside them are left out: they show the rest when
// they scroll, or the document asked for the cut. So is a chart that fills
// its share, which draws at any height.
func (s *Surface) cutBy(b *box) int {
	total := 0
	for _, k := range b.kids {
		if k.hidden || k.pgen != s.gen {
			continue
		}
		if k.height.kind != sizeAuto || k.maxH.kind != sizeAuto {
			continue
		}
		if k.kind == "spacer" || k.pw <= 0 {
			continue
		}
		switch w := k.w.(type) {
		case *tableWidget, *listWidget, *scrollWidget:
			// A box that scrolls is cut only when nothing of it shows.
			if k.rect.Empty() {
				_, h := s.measure(k, k.pw)
				total += min(h, 3)
			}
			continue
		case *cardWidget:
			if w.folded {
				continue
			}
		}
		_, h := s.measure(k, k.pw)
		if len(k.kids) > 0 && !k.rect.Empty() {
			total += s.cutBy(k)
			continue
		}
		// A leaf, or a box squeezed to nothing with all inside it, and
		// then the gap before it too.
		if h > k.rect.Dy() {
			total += h - k.rect.Dy()
			if k.rect.Empty() {
				total += b.gap
			}
		}
	}
	return total
}

// pageKey moves the page for a key the focused box did not take. It
// reports whether the key moved it, or would have at the page's end.
func (s *Surface) pageKey(key string) bool {
	pw := s.pageOf()
	if pw == nil || !pw.over {
		return false
	}
	switch key {
	case "space":
		key = "pgdown"
	case "shift+space":
		key = "pgup"
	}
	limit := max(pw.content-s.h, 0)
	next, ok := cursorMove(key, s.view.Page, limit+1, max(s.h-1, 1))
	if !ok {
		return false
	}
	s.scrollPage(next)
	return true
}

// scrollPage moves the page to off rows down.
func (s *Surface) scrollPage(off int) {
	pw := s.pageOf()
	if pw == nil {
		return
	}
	off = clampInt(off, 0, max(pw.content-s.h, 0))
	if off == s.view.Page {
		return
	}
	s.view.Page = off
	s.view.PageAnchor = ""
	s.relayout = true
}

// focusRect is the part of a focused box to keep in view, in page rows: a
// table's cursor row with the header when they fit together, a list's
// cursor item, or else the box.
func focusRect(b *box) Rect {
	r := b.rect
	switch w := b.w.(type) {
	case *tableWidget:
		y := w.bodyTop + w.cursor - w.offset
		row := rect(r.Min.X, y, r.Dx(), 1)
		if y+1-r.Min.Y <= r.Dy() {
			row.Min.Y = r.Min.Y
		}
		return row
	case *listWidget:
		if w.cursor >= 0 && w.cursor < len(b.kids) {
			return b.kids[w.cursor].rect
		}
	}
	return r
}

// scrollersShort returns how many rows the tables, lists and scroll boxes
// inside b lack to show their rows, up to two less than the surface's height
// each.
func (s *Surface) scrollersShort(b *box) int {
	total := 0
	for _, k := range b.kids {
		if k.hidden || k.pgen != s.gen || k.pw <= 0 || k.height.kind != sizeAuto || k.maxH.kind != sizeAuto {
			continue
		}
		switch w := k.w.(type) {
		case *tableWidget, *listWidget, *scrollWidget:
			_, h := s.measure(k, k.pw)
			total += max(min(h, s.h-2)-k.rect.Dy(), 0)
			continue
		case *cardWidget:
			if w.folded {
				continue
			}
		}
		total += s.scrollersShort(k)
	}
	return total
}
