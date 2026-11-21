package loom

type pageWidget struct {
	over    bool
	content int
}

func (w *pageWidget) measure(s *Surface, b *box, maxW int) (int, int) {
	return s.measureCol(b.kids, maxW, b.gap)
}

func (w *pageWidget) paint(s *Surface, b *box, c *canvas, r Rect) {
	if w.over {
		paintBar(s, c, r, w.content, s.view.Page)
	}
}

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
	for y := 0; y < r.Dy(); y++ {
		g, st := s.g.scrollTrack, style{fg: "mute"}
		if y >= top && y < top+thumb {
			g, st = s.g.scrollTh, style{fg: "dim"}
		}
		c.text(x, r.Min.Y+y, g, st, 1)
	}
}

func (s *Surface) pageOf() *pageWidget {
	if s.root == nil {
		return nil
	}
	pw, _ := s.root.w.(*pageWidget)
	return pw
}

func (s *Surface) layoutPage() {
	pw := s.pageOf()
	if pw == nil {
		return
	}
	pw.over = false
	if s.w < 2 || s.h < 1 || s.cutBy(s.root) == 0 {
		return
	}
	w := s.w - 1
	natural := s.pageHeight(w)
	if natural <= s.h {
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
		page = natural
		s.placeRoot(0, w, page)
	}
	if short := s.scrollersShort(s.root); short > 0 && page < natural {
		page = min(page+short, natural)
		s.placeRoot(0, w, page)
	}
	pw.over, pw.content = true, page
	off := s.view.Page
	if b := s.placedBox(s.root, s.view.PageAnchor); b != nil {
		off = b.rect.Min.Y + min(s.view.PageRow, max(b.rect.Dy()-1, 0))
	}
	off = clampInt(off, 0, page-s.h)
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

func (s *Surface) pageHeight(w int) int {
	if len(s.root.kids) == 1 && s.root.kids[0].height.kind == sizeAuto {
		k := s.root.kids[0]
		_, h := s.measure(k, s.colChildWidth(s.root, k, w))
		return h
	}
	_, h := s.measureCol(s.root.kids, w, s.root.gap)
	return h
}

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
		if h > k.rect.Dy() {
			total += h - k.rect.Dy()
			if k.rect.Empty() {
				total += b.gap
			}
		}
	}
	return total
}

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
