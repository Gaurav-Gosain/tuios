package loom

import "slices"

type item struct {
	base, weight int
	lo, hi       int
	shrink       bool
	elastic      bool
	give         int
}

func distribute(avail int, items []item) []int {
	out := make([]int, len(items))
	used, weights := 0, 0
	for i, it := range items {
		out[i] = clampSize(it.base, it.lo, it.hi)
		used += out[i]
		weights += it.weight
	}
	free := avail - used
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
	for pass := 0; pass < 3 && free < 0; pass++ {
		free = shrinkItems(items, out, free, pass)
	}
	return out
}

func (s *Surface) dropNarrowCharts(kids []*box, items []item, out []int) {
	freed := 0
	for i, k := range kids {
		if elasticKind(k.kind) && out[i] > 0 && out[i] < min(minChartWidthOf(k.kind), items[i].base) {
			freed += out[i]
			out[i] = 0
		}
	}
	for i, k := range kids {
		if freed == 0 {
			break
		}
		if k.kind == "spacer" || elasticKind(k.kind) {
			continue
		}
		if fixed := k.width.resolve(0); fixed >= 0 || k.width.kind == sizeFill {
			continue
		}
		want, _ := s.measure(k, out[i]+freed)
		if add := min(max(want-out[i], 0), freed); add > 0 {
			out[i] += add
			freed -= add
		}
	}
}

func shrinkItems(items []item, out []int, free int, pass int) int {
	may := func(it item) bool {
		switch pass {
		case 0:
			return it.shrink && it.give > 0
		case 1:
			return it.shrink && it.elastic
		}
		return it.shrink
	}
	floor := func(it item) int {
		if pass == 0 {
			return max(it.lo, it.base-it.give, 0)
		}
		return max(it.lo, 0)
	}
	for round := 0; free < 0 && round < 4; round++ {
		total := 0
		for i, it := range items {
			if may(it) && out[i] > floor(it) {
				total += out[i] - floor(it)
			}
		}
		if total == 0 {
			break
		}
		need := -free
		for i, it := range items {
			if !may(it) || out[i] <= floor(it) {
				continue
			}
			room := out[i] - floor(it)
			cut := min(room, (need*room+total-1)/total)
			out[i] -= cut
			free += cut
			if free >= 0 {
				break
			}
		}
	}
	return free
}

const minChartWidth = 8

const minGaugeWidth = 16

func minChartWidthOf(kind string) int {
	if kind == "gauge" || kind == "progress" {
		return minGaugeWidth
	}
	return minChartWidth
}

func elasticKind(kind string) bool {
	switch kind {
	case "gauge", "progress", "sparkline", "bar", "line", "area", "histogram":
		return true
	}
	return false
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

func bounds(lo, hi size, parent int) (int, int) {
	l, h := lo.resolve(parent), hi.resolve(parent)
	return max(l, 0), h
}

func (s *Surface) measure(b *box, maxW int) (int, int) {
	maxW = max(maxW, 0)
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
	default:
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
	if lines := s.rowLines(b, inner); lines != nil {
		ch := 0
		for _, line := range lines {
			ch += s.lineHeight(line, s.rowWidths(line, b.gap, inner))
		}
		return inner, ch
	}
	widths := s.rowWidths(b.kids, b.gap, inner)
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

func (s *Surface) lineHeight(kids []*box, widths []int) int {
	h := 0
	for i, k := range kids {
		_, kh := s.measure(k, widths[i])
		h = max(h, kh)
	}
	return h
}

func (s *Surface) rowLines(b *box, inner int) [][]*box {
	if len(b.kids) < 2 || !slices.ContainsFunc(b.kids, func(k *box) bool { return k.kind == "button" || k.kind == "stat" }) {
		return nil
	}
	natural := func(k *box) int {
		if fixed := k.width.resolve(inner); fixed >= 0 {
			return fixed
		}
		if k.kind == "spacer" {
			return 0
		}
		w, _ := s.measure(k, inner)
		return w
	}
	total := 0
	for i, k := range b.kids {
		total += natural(k)
		if i > 0 {
			total += b.gap
		}
	}
	if total <= inner {
		return nil
	}
	var lines [][]*box
	var cur, pending []*box
	curW := 0
	for _, k := range b.kids {
		if k.kind == "spacer" {
			pending = append(pending, k)
			continue
		}
		w := natural(k)
		if len(cur) > 0 && curW+b.gap*(len(pending)+1)+w > inner {
			lines = append(lines, cur)
			cur, curW = nil, 0
		}
		for _, p := range pending {
			if len(cur) > 0 {
				curW += b.gap
			}
			cur = append(cur, p)
		}
		pending = nil
		if len(cur) > 0 {
			curW += b.gap
		}
		cur = append(cur, k)
		curW += w
	}
	cur = append(cur, pending...)
	lines = append(lines, cur)
	if len(lines) < 2 {
		return nil
	}
	return lines
}

func (s *Surface) rowWidths(kids []*box, gap, inner int) []int {
	n := len(kids)
	if n == 0 {
		return nil
	}
	avail := max(inner-gap*(n-1), 0)
	items := make([]item, n)
	for i, k := range kids {
		lo, hi := bounds(k.minW, k.maxW, avail)
		it := item{lo: lo, hi: hi, weight: k.grow}
		switch fixed := k.width.resolve(avail); {
		case fixed >= 0:
			it.base = fixed
			if elasticKind(k.kind) {
				it.shrink, it.elastic = true, true
			}
		case k.width.kind == sizeFill:
			it.weight = max(it.weight, k.width.n)
		case k.kind == "spacer":
			it.weight = max(it.weight, 1)
		default:
			it.base, _ = s.measure(k, avail)
			it.shrink = true
			it.elastic = elasticKind(k.kind)

		}
		items[i] = it
	}
	out := distribute(avail, items)
	s.dropNarrowCharts(kids, items, out)
	return out
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

func (s *Surface) layoutRoot() {
	s.gen++
	s.root.rect = rect(0, 0, s.w, s.h)
	s.root.ground = s.ground()
	s.placeRoot(0, s.w, s.h)
	s.layoutPage()
}

func (s *Surface) placeRoot(off, w, h int) {
	s.focus = s.focus[:0]
	if len(s.root.kids) == 1 && s.root.kids[0].height.kind == sizeAuto {
		k := s.root.kids[0]
		s.place(k, rect(0, -off, s.colChildWidth(s.root, k, w), h))
		return
	}
	s.placeCol(s.root, rect(0, -off, w, h))
}

func (s *Surface) place(b *box, r Rect) {
	b.pw = r.Dx()
	if p := b.parent; p != nil && p != s.root && !scrolls(p) {
		r = r.Intersect(viewport(p))
	}
	b.rect, b.pgen = r, s.gen
	if b.ground == "" && b.parent != nil {
		b.ground = b.parent.ground
	}
	if b.hidden {
		return
	}
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

type placer interface {
	place(s *Surface, b *box, inner Rect)
}

func (s *Surface) placeRow(b *box, in Rect) {
	lines := s.rowLines(b, in.Dx())
	if lines == nil {
		s.placeLine(b, b.kids, s.rowWidths(b.kids, b.gap, in.Dx()), in)
		return
	}
	y := in.Min.Y
	for _, line := range lines {
		widths := s.rowWidths(line, b.gap, in.Dx())
		h := min(s.lineHeight(line, widths), max(in.Max.Y-y, 0))
		s.placeLine(b, line, widths, rect(in.Min.X, y, in.Dx(), h))
		y += h
	}
}

func (s *Surface) placeLine(b *box, kids []*box, widths []int, in Rect) {
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
	for i, k := range kids {
		wd := widths[i]
		h := in.Dy()
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
			if shrinks(k) {
				it.shrink = true
				it.lo = max(it.lo, min(it.base, 3))
				it.give = s.give(k, widths[i], it.base)
			}
		}
		items[i] = it
	}
	return distribute(avail, items)
}

func (s *Surface) give(b *box, w, h int) int {
	if b.hidden {
		return 0
	}
	switch b.kind {
	case "table", "list", "scroll":
		return max(h-min(h, 3), 0)
	case "col", "form", "tab", "card", "tabs":
	default:
		return 0
	}
	if cw, ok := b.w.(*cardWidget); ok && (cw.folded || cw.modal) {
		return 0
	}
	if b.height.kind != sizeAuto {
		return 0
	}
	inner := max(w-b.padW(), 0)
	if cw, ok := b.w.(*cardWidget); ok {
		inner = max(inner-cw.indent, 0)
	}
	total := 0
	for _, k := range b.kids {
		if k.height.kind != sizeAuto {
			continue
		}
		_, kh := s.measure(k, s.colChildWidth(b, k, inner))
		total += s.give(k, s.colChildWidth(b, k, inner), kh)
	}
	return min(total, h)
}

func shrinks(b *box) bool {
	switch b.kind {
	case "table", "list", "scroll", "code", "markdown", "diff", "mermaid", "text", "card", "tabs", "tab", "col", "stack":
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

func visible(b *box) bool {
	r := b.rect
	for p := b.parent; p != nil && !r.Empty(); p = p.parent {
		r = r.Intersect(viewport(p))
	}
	return !r.Empty()
}
