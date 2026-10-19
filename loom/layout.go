package loom

// Layout runs in two passes. measure returns a box's natural size within a
// width. place gives each box its rectangle, from the root down, sharing
// space along a row or a column the way flex boxes do: fixed and percent
// sizes first, then natural sizes, then the free space by fill weight and
// grow, and when space is short the boxes that can shrink give it back.

// item is one child on the main axis of a row or column.
type item struct {
	base, weight int
	lo, hi       int // min and max; hi < 0 means none
	shrink       bool
}

// distribute shares avail cells among items and returns each one's size.
func distribute(avail int, items []item) []int {
	out := make([]int, len(items))
	used, weights := 0, 0
	for i, it := range items {
		out[i] = clampSize(it.base, it.lo, it.hi)
		used += out[i]
		weights += it.weight
	}
	free := avail - used
	// Grow by weight, a few rounds so a capped item passes its share on.
	for round := 0; free > 0 && weights > 0 && round < 4; round++ {
		share, rem := free/weights, free%weights
		given, nextWeights := 0, 0
		for i, it := range items {
			if it.weight == 0 {
				continue
			}
			add := share * it.weight
			if rem > 0 {
				extra := min(rem, it.weight)
				add += extra
				rem -= extra
			}
			next := clampSize(out[i]+add, it.lo, it.hi)
			given += next - out[i]
			out[i] = next
			if it.hi < 0 || out[i] < it.hi {
				nextWeights += it.weight
			}
		}
		free -= given
		weights = nextWeights
		if given == 0 {
			break
		}
	}
	// Shrink what may shrink, in proportion to its size.
	for round := 0; free < 0 && round < 4; round++ {
		total := 0
		for i, it := range items {
			if it.shrink && out[i] > it.lo {
				total += out[i] - max(it.lo, 0)
			}
		}
		if total == 0 {
			break
		}
		need := -free
		for i, it := range items {
			if !it.shrink || out[i] <= it.lo {
				continue
			}
			room := out[i] - max(it.lo, 0)
			cut := min(room, (need*room+total-1)/total)
			out[i] -= cut
			free += cut
			if free >= 0 {
				break
			}
		}
	}
	return out
}

func clampSize(v, lo, hi int) int {
	if hi >= 0 && v > hi {
		v = hi
	}
	if v < lo {
		v = lo
	}
	return max(v, 0)
}

// bounds returns the min and max a pair of size attributes give against
// the parent's size.
func bounds(lo, hi size, parent int) (int, int) {
	l, h := lo.resolve(parent), hi.resolve(parent)
	return max(l, 0), h
}

// measure returns the natural size of b within maxW cells, padding
// included. The width is at most maxW.
func (s *Surface) measure(b *box, maxW int) (int, int) {
	maxW = max(maxW, 0)
	// One layout pass measures a box at a width once. Without the cache a
	// chain of rows costs 2^depth measures: each row measures a child to
	// share the width, then again for its height.
	if b.mgen != s.gen {
		b.mgen, b.mcache = s.gen, b.mcache[:0]
	}
	for _, e := range b.mcache {
		if e[0] == maxW {
			return e[1], e[2]
		}
	}
	w, h := s.measureNew(b, maxW)
	b.mcache = append(b.mcache, [3]int{maxW, w, h})
	return w, h
}

func (s *Surface) measureNew(b *box, maxW int) (int, int) {
	s.stats.measures++
	if fixed := b.width.resolve(maxW); fixed >= 0 {
		maxW = min(maxW, fixed)
	}
	lo, hi := bounds(b.minW, b.maxW, maxW)
	if hi >= 0 {
		maxW = min(maxW, hi)
	}
	inner := max(maxW-b.padW(), 0)
	var cw, ch int
	switch {
	case b.w != nil:
		cw, ch = b.w.measure(s, b, inner)
	case b.kind == "row":
		cw, ch = s.measureRow(b, inner)
	case b.kind == "grid":
		cw, ch = s.measureGrid(b, inner)
	case b.kind == "stack":
		for _, k := range b.kids {
			kw, kh := s.measure(k, inner)
			cw, ch = max(cw, kw), max(ch, kh)
		}
	case b.kind == "split":
		cw = inner
		for _, k := range b.kids {
			_, kh := s.measure(k, inner)
			ch = max(ch, kh)
		}
	case b.kind == "spacer":
	default: // col, form, tab, tml and unknown containers stack their children
		cw, ch = s.measureCol(b.kids, inner, b.gap)
	}
	w := cw + b.padW()
	if fixed := b.width.resolve(maxW); fixed >= 0 {
		w = fixed
	} else if b.width.kind == sizeFill {
		w = maxW
	}
	w = min(clampSize(w, lo, hi), maxW)
	h := ch + b.padH()
	if fixed := b.height.resolve(0); fixed >= 0 && b.height.kind == sizeCells {
		h = fixed
	}
	hlo, hhi := bounds(b.minH, b.maxH, 0)
	return w, clampSize(h, hlo, hhi)
}

// measureCol returns the size of boxes stacked top to bottom.
func (s *Surface) measureCol(kids []*box, inner, gap int) (int, int) {
	cw, ch := 0, 0
	for i, k := range kids {
		kw, kh := s.measure(k, inner)
		cw = max(cw, kw)
		ch += kh
		if i > 0 {
			ch += gap
		}
	}
	return cw, ch
}

func (s *Surface) measureRow(b *box, inner int) (int, int) {
	widths := s.rowWidths(b, inner)
	cw, ch, fill := 0, 0, false
	for i, k := range b.kids {
		cw += widths[i]
		if i > 0 {
			cw += b.gap
		}
		if k.kind == "spacer" || k.width.kind == sizeFill || k.grow > 0 {
			fill = true
		}
		_, kh := s.measure(k, widths[i])
		ch = max(ch, kh)
	}
	if fill {
		cw = inner
	}
	return min(cw, inner), ch
}

// rowWidths shares a row's inner width among its children.
func (s *Surface) rowWidths(b *box, inner int) []int {
	n := len(b.kids)
	if n == 0 {
		return nil
	}
	avail := max(inner-b.gap*(n-1), 0)
	items := make([]item, n)
	for i, k := range b.kids {
		lo, hi := bounds(k.minW, k.maxW, avail)
		it := item{lo: lo, hi: hi, weight: k.grow}
		switch fixed := k.width.resolve(avail); {
		case fixed >= 0:
			it.base = fixed
		case k.width.kind == sizeFill:
			it.weight = max(it.weight, k.width.n)
		case k.kind == "spacer":
			it.weight = max(it.weight, 1)
		default:
			it.base, _ = s.measure(k, avail)
			it.shrink = true
		}
		items[i] = it
	}
	return distribute(avail, items)
}

func (s *Surface) measureGrid(b *box, inner int) (int, int) {
	cols := s.gridCols(b)
	colW := max((inner-b.gap*(cols-1))/cols, 0)
	ch := 0
	for r := 0; r*cols < len(b.kids); r++ {
		rowH := 0
		for c := 0; c < cols && r*cols+c < len(b.kids); c++ {
			_, kh := s.measure(b.kids[r*cols+c], colW)
			rowH = max(rowH, kh)
		}
		if r > 0 {
			ch += b.gap
		}
		ch += rowH
	}
	return inner, ch
}

func (s *Surface) gridCols(b *box) int {
	n := b.src.AttrString("cols")
	v := parseSize(n)
	if v.kind != sizeCells || v.n < 1 {
		return 1
	}
	return min(v.n, 24)
}

// layoutRoot places the whole tree in the surface.
func (s *Surface) layoutRoot() {
	s.focus = s.focus[:0]
	s.gen++
	s.root.rect = rect(0, 0, s.w, s.h)
	s.root.ground = s.ground()
	// The document's one top box fills the surface, unless it asks for a
	// height of its own.
	if len(s.root.kids) == 1 && s.root.kids[0].height.kind == sizeAuto {
		k := s.root.kids[0]
		s.place(k, rect(0, 0, s.colChildWidth(s.root, k, s.w), s.h))
		return
	}
	s.placeCol(s.root, s.root.rect)
}

// place gives b its rectangle and places its children inside it.
func (s *Surface) place(b *box, r Rect) {
	// A box never reaches past its parent's content area, so siblings
	// never overlap and a box can repaint alone. Inside a scroll the
	// children move past the edges and the clip cuts them instead.
	if p := b.parent; p != nil && p != s.root && !scrolls(p) {
		r = r.Intersect(viewport(p))
	}
	b.rect = r
	if b.ground == "" && b.parent != nil {
		b.ground = b.parent.ground
	}
	if b.hidden {
		// show is false: the box keeps its space, and nothing inside it is
		// laid out, takes the focus, a key or a click.
		return
	}
	// Every box that takes keys is in the focus order, on the screen or
	// not: tab reaches a button below the fold, and a scroll brings the
	// focused box into view. Shortcuts and clicks need the box on screen.
	if focusable(s, b) {
		s.focus = append(s.focus, b)
	}
	in := b.inner()
	switch {
	case b.w != nil:
		if p, ok := b.w.(placer); ok {
			p.place(s, b, in)
		}
	case b.kind == "row":
		s.placeRow(b, in)
	case b.kind == "grid":
		s.placeGrid(b, in)
	case b.kind == "stack":
		for _, k := range b.kids {
			s.place(k, in)
		}
	case b.kind == "split":
		s.placeSplit(b, in)
	default:
		s.placeCol(b, in)
	}
}

// placer is a widget that holds children and places them itself.
type placer interface {
	place(s *Surface, b *box, inner Rect)
}

func (s *Surface) placeRow(b *box, in Rect) {
	widths := s.rowWidths(b, in.Dx())
	used := 0
	for i, wd := range widths {
		used += wd
		if i > 0 {
			used += b.gap
		}
	}
	x, step := in.Min.X, 0
	free := max(in.Dx()-used, 0)
	switch b.justify {
	case "center":
		x += free / 2
	case "end":
		x += free
	case "between":
		if len(widths) > 1 {
			step = free / (len(widths) - 1)
		}
	case "around":
		if len(widths) > 0 {
			step = free / len(widths)
			x += step / 2
		}
	}
	for i, k := range b.kids {
		wd := widths[i]
		h := in.Dy()
		// fill on the cross axis stretches, as align="stretch" does.
		if (b.align != "stretch" && b.align != "" || k.height.kind != sizeAuto) && k.height.kind != sizeFill {
			_, kh := s.measure(k, wd)
			if fixed := k.height.resolve(in.Dy()); fixed >= 0 {
				kh = fixed
			}
			h = min(kh, in.Dy())
		}
		y := in.Min.Y
		switch b.align {
		case "center":
			y += (in.Dy() - h) / 2
		case "end":
			y += in.Dy() - h
		}
		s.place(k, rect(x, y, wd, h))
		x += wd + b.gap + step
	}
}

// colHeights shares a column's inner height among boxes of the given
// widths.
func (s *Surface) colHeights(kids []*box, widths []int, in Rect, gap int) []int {
	n := len(kids)
	avail := max(in.Dy()-gap*max(n-1, 0), 0)
	items := make([]item, n)
	for i, k := range kids {
		lo, hi := bounds(k.minH, k.maxH, avail)
		it := item{lo: lo, hi: hi, weight: k.grow}
		switch fixed := k.height.resolve(avail); {
		case fixed >= 0:
			it.base = fixed
		case k.height.kind == sizeFill:
			it.weight = max(it.weight, k.height.n)
		case k.kind == "spacer":
			it.weight = max(it.weight, 1)
		default:
			_, it.base = s.measure(k, widths[i])
			// A box that scrolls can give rows back.
			if shrinks(k) {
				it.shrink = true
				it.lo = max(it.lo, min(it.base, 3))
			}
		}
		items[i] = it
	}
	return distribute(avail, items)
}

func shrinks(b *box) bool {
	switch b.kind {
	case "table", "list", "scroll", "code", "markdown", "diff", "mermaid", "text", "card", "tabs", "tab", "col":
		return true
	}
	return false
}

func (s *Surface) placeCol(b *box, in Rect) {
	widths := make([]int, len(b.kids))
	for i, k := range b.kids {
		widths[i] = s.colChildWidth(b, k, in.Dx())
	}
	heights := s.colHeights(b.kids, widths, in, b.gap)
	used := 0
	for i, h := range heights {
		used += h
		if i > 0 {
			used += b.gap
		}
	}
	y, step := in.Min.Y, 0
	free := max(in.Dy()-used, 0)
	switch b.justify {
	case "center":
		y += free / 2
	case "end":
		y += free
	case "between":
		if len(heights) > 1 {
			step = free / (len(heights) - 1)
		}
	}
	for i, k := range b.kids {
		x := in.Min.X
		switch b.align {
		case "center":
			x += (in.Dx() - widths[i]) / 2
		case "end":
			x += in.Dx() - widths[i]
		}
		s.place(k, rect(x, y, widths[i], heights[i]))
		y += heights[i] + b.gap + step
	}
}

// colChildWidth is a child's width in a column: its own w, or the column's
// width unless the column aligns its children.
func (s *Surface) colChildWidth(col, k *box, inner int) int {
	if fixed := k.width.resolve(inner); fixed >= 0 {
		lo, hi := bounds(k.minW, k.maxW, inner)
		return min(clampSize(fixed, lo, hi), inner)
	}
	if col.align == "" || col.align == "stretch" || k.width.kind == sizeFill {
		lo, hi := bounds(k.minW, k.maxW, inner)
		return min(clampSize(inner, lo, hi), inner)
	}
	w, _ := s.measure(k, inner)
	return w
}

func (s *Surface) placeGrid(b *box, in Rect) {
	cols := s.gridCols(b)
	colW := max((in.Dx()-b.gap*(cols-1))/cols, 0)
	y := in.Min.Y
	for r := 0; r*cols < len(b.kids); r++ {
		rowH := 0
		for c := 0; c < cols && r*cols+c < len(b.kids); c++ {
			_, kh := s.measure(b.kids[r*cols+c], colW)
			rowH = max(rowH, kh)
		}
		for c := 0; c < cols && r*cols+c < len(b.kids); c++ {
			s.place(b.kids[r*cols+c], rect(in.Min.X+c*(colW+b.gap), y, colW, rowH))
		}
		y += rowH + b.gap
	}
}

func (s *Surface) placeSplit(b *box, in Rect) {
	if len(b.kids) == 0 {
		return
	}
	ratio := s.splitRatio(b)
	if b.src.AttrString("dir") == "v" {
		first := int(float64(in.Dy()-1) * ratio)
		s.place(b.kids[0], rect(in.Min.X, in.Min.Y, in.Dx(), first))
		if len(b.kids) > 1 {
			s.place(b.kids[1], rect(in.Min.X, in.Min.Y+first+1, in.Dx(), in.Dy()-first-1))
		}
		return
	}
	first := int(float64(in.Dx()-1) * ratio)
	s.place(b.kids[0], rect(in.Min.X, in.Min.Y, first, in.Dy()))
	if len(b.kids) > 1 {
		s.place(b.kids[1], rect(in.Min.X+first+1, in.Min.Y, in.Dx()-first-1, in.Dy()))
	}
}

// splitRatio is the first child's share: the one a person dragged to, or
// the ratio attribute, or a half.
func (s *Surface) splitRatio(b *box) float64 {
	if r, ok := lookup(s.view.Split, b); ok {
		return r
	}
	v, ok := b.src.Attr("ratio")
	if f, isNum := v.Float(); ok && isNum && f > 0 && f < 1 {
		return f
	}
	return 0.5
}

// visible reports whether some of b is on the screen: its rectangle meets
// the viewport of every box above it. A box that a short column squeezed
// to nothing, or that a scroll moved out of view, is not.
func visible(b *box) bool {
	r := b.rect
	for p := b.parent; p != nil && !r.Empty(); p = p.parent {
		r = r.Intersect(viewport(p))
	}
	return !r.Empty()
}
