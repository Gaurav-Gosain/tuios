package loom

import (
	"strconv"

	"tuios.dev/tml"
)

// cardWidget is a titled box in the cockpit card style: a fold mark, the
// title, the meta in the card's role, then the children indented. A modal
// draws the same way inside a frame.
type cardWidget struct {
	title, meta, role string
	folded            bool
	modal             bool
	indent            int // 2, or 0 when the body starts with a cursor margin of its own
	press             []tml.Action
}

func (s *Surface) buildCard(b *box, vars map[string]any) widget {
	n := b.src
	w := &cardWidget{title: s.str(n, "title", vars), meta: s.str(n, "meta", vars),
		role: s.roleOr(n, vars, "accent-bright"), modal: n.Type == "modal", press: s.actionsOf(n, "on-press")}
	if f, ok := lookup(s.view.Fold, b); ok {
		w.folded = f
	} else {
		w.folded = s.flag(n, "fold", vars, false)
	}
	if !w.folded {
		b.kids = s.buildChildren(n, b.ptr, b.scope, b)
	}
	w.indent = 2
	if len(b.kids) > 0 {
		switch k := b.kids[0].w.(type) {
		case *tableWidget:
			if k.selectable {
				w.indent = 0
			}
		case *listWidget:
			if k.selectable {
				w.indent = 0
			}
		}
	}
	return w
}

// body returns the rectangle of the children inside the card's inner
// rectangle.
func (w *cardWidget) body(in Rect) Rect {
	if w.modal {
		return rect(in.Min.X+2, in.Min.Y+1, in.Dx()-4, in.Dy()-2)
	}
	return rect(in.Min.X+w.indent, in.Min.Y+1, in.Dx()-w.indent, in.Dy()-1)
}

func (w *cardWidget) measure(s *Surface, b *box, maxW int) (int, int) {
	if w.modal {
		cw, ch := s.measureCol(b.kids, max(maxW-4, 0), b.gap)
		return min(max(cw+4, width(w.title)+6), maxW), ch + 2
	}
	if w.folded {
		return min(4+width(w.title)+width(w.meta), maxW), 1
	}
	cw, ch := s.measureCol(b.kids, max(maxW-w.indent, 0), b.gap)
	return min(max(cw+w.indent, width(w.title)+4+width(w.meta)), maxW), ch + 1
}

func (w *cardWidget) place(s *Surface, b *box, in Rect) {
	if !w.folded {
		s.placeCol(b, w.body(in))
	}
}

func (w *cardWidget) paint(s *Surface, b *box, c *canvas, r Rect) {
	if w.modal {
		s.frame(c, r, w.title, "", "edge")
		return
	}
	mark := s.g.foldOpen
	if w.folded {
		mark = s.g.foldShut
	}
	l := line{{text: mark + " ", st: style{fg: "mute"}}, {text: w.title, st: style{fg: "fg", attrs: attrBold}}}
	if w.meta != "" {
		l = append(l, span{text: "  " + w.meta, st: style{fg: w.role, attrs: attrBold}})
	}
	c.drawLine(r.Min.X, r.Min.Y, r.Dx(), l, alignLeft)
}

// frame draws a rounded border on r with a title on the top edge and a
// label at the right of it, the frame a host puts around a popup.
func (s *Surface) frame(c *canvas, r Rect, title, right, edge string) {
	if r.Dx() < 2 || r.Dy() < 2 {
		return
	}
	g := s.g
	st := style{fg: edge}
	c.text(r.Min.X, r.Min.Y, g.tl, st, 1)
	c.hline(r.Min.X+1, r.Min.Y, r.Dx()-2, g.hline, st)
	c.text(r.Max.X-1, r.Min.Y, g.tr, st, 1)
	for y := r.Min.Y + 1; y < r.Max.Y-1; y++ {
		c.text(r.Min.X, y, g.vline, st, 1)
		c.text(r.Max.X-1, y, g.vline, st, 1)
	}
	c.text(r.Min.X, r.Max.Y-1, g.bl, st, 1)
	c.hline(r.Min.X+1, r.Max.Y-1, r.Dx()-2, g.hline, st)
	c.text(r.Max.X-1, r.Max.Y-1, g.br, st, 1)
	room := r.Dx() - 4
	if right != "" && room > 8 {
		rt := truncate(" "+right+" ", room/2, g.ellipsis)
		c.textRight(r.Max.X-2, r.Min.Y, rt, style{fg: "dim"})
		room -= width(rt) + 1
	}
	if title != "" && room > 2 {
		c.text(r.Min.X+2, r.Min.Y, truncate(" "+title+" ", room, g.ellipsis), style{fg: "fg", attrs: attrBold}, -1)
	}
}

// tabsWidget shows a strip of tab titles and the open tab. Only the open
// tab is built.
type tabsWidget struct {
	ids, titles []string
	active      int
	statePath   string
	change      []tml.Action
	spans       [][2]int // the x range of each title at the last paint
}

func (s *Surface) buildTabs(b *box, vars map[string]any) widget {
	n := b.src
	w := &tabsWidget{statePath: n.AttrString("value"), change: s.actionsOf(n, "on-change")}
	var tabs []*tml.Node
	var ptrs []string
	for i, c := range n.Children {
		if c.Type != "tab" || !s.truthy(c, "if", vars) {
			continue
		}
		tabs = append(tabs, c)
		ptrs = append(ptrs, b.ptr+"/children/"+strconv.Itoa(i))
		w.ids = append(w.ids, c.ID())
		w.titles = append(w.titles, s.str(c, "title", vars))
	}
	open := ""
	if w.statePath != "" {
		if ptr, err := tml.PathPointer(w.statePath); err == nil {
			open = tml.Stringify(s.store.Get(ptr))
		}
	} else {
		open, _ = lookup(s.view.Tabs, b)
	}
	for i, id := range w.ids {
		if id == open {
			w.active = i
		}
	}
	if len(tabs) > 0 {
		if t := s.buildOne(tabs[w.active], ptrs[w.active], b.scope, b); t != nil {
			b.kids = []*box{t}
		}
	}
	return w
}

func (w *tabsWidget) measure(s *Surface, b *box, maxW int) (int, int) {
	_, ch := s.measureCol(b.kids, maxW, 0)
	return maxW, 3 + ch
}

func (w *tabsWidget) place(s *Surface, b *box, in Rect) {
	for _, k := range b.kids {
		s.place(k, rect(in.Min.X, in.Min.Y+3, in.Dx(), in.Dy()-3))
	}
}

func (w *tabsWidget) paint(s *Surface, _ *box, c *canvas, r Rect) {
	x := r.Min.X
	w.spans = w.spans[:0]
	for i, t := range w.titles {
		st := style{fg: "mute"}
		if i == w.active {
			st = style{fg: "fg", attrs: attrBold}
		}
		end := c.text(x, r.Min.Y, t, st, r.Max.X-x)
		w.spans = append(w.spans, [2]int{x, end})
		if i == w.active {
			c.hline(x, r.Min.Y+1, end-x, s.g.tabRule, style{fg: "accent"})
		}
		x = end + 4
	}
	for xx := r.Min.X; xx < r.Max.X; xx++ {
		if i := w.titleAt(xx); i < 0 || i != w.active {
			c.text(xx, r.Min.Y+1, s.g.ruleSoft, style{fg: "edge"}, 1)
		}
	}
}

// titleAt returns the tab whose title covers column x, or -1.
func (w *tabsWidget) titleAt(x int) int {
	for i, sp := range w.spans {
		if x >= sp[0] && x < sp[1] {
			return i
		}
	}
	return -1
}

// listItem is the kind of the box that holds one element's children in a
// list. Its src is the list node, so it is never built on its own: a
// rebuild of an item rebuilds the list.
const listItem = "list-item"

// listWidget repeats its children once per element of a state array.
// Each element's children sit in a group box. With select, a cursor marks
// one group.
type listWidget struct {
	selectable bool
	items      []any
	cursor     int
	offset     int
}

func (s *Surface) buildList(b *box, vars map[string]any) widget {
	n := b.src
	w := &listWidget{selectable: n.AttrString("select") == "single" || n.AttrString("select") == "multi"}
	w.items = s.data(n, "items", vars)
	alias := n.AttrString("as")
	if alias == "" {
		alias = "item"
	}
	for i, it := range w.items {
		if !s.exp.Take(1) {
			w.items = w.items[:i]
			break
		}
		g := &box{kind: listItem, src: n, ptr: b.ptr, key: b.key + "#" + strconv.Itoa(i), base: b.base, parent: b, scope: b.scope.with(alias, it, i), dirty: true}
		g.kids = s.buildChildren(n, b.ptr, g.scope, g)
		b.kids = append(b.kids, g)
	}
	if cur, ok := lookup(s.view.Cursor, b); ok {
		w.cursor, _ = strconv.Atoi(cur)
	}
	w.cursor = clampInt(w.cursor, 0, max(len(w.items)-1, 0))
	w.offset, _ = lookup(s.view.Scroll, b)
	return w
}

func (w *listWidget) indent() int {
	if w.selectable {
		return 2
	}
	return 0
}

func (w *listWidget) measure(s *Surface, b *box, maxW int) (int, int) {
	cw, ch := s.measureCol(b.kids, max(maxW-w.indent(), 0), b.gap)
	return min(cw+w.indent(), maxW), ch
}

func (w *listWidget) place(s *Surface, b *box, in Rect) {
	inner := rect(in.Min.X+w.indent(), in.Min.Y, in.Dx()-w.indent(), in.Dy())
	// Keep the cursor's group in view.
	heights := make([]int, len(b.kids))
	tops := make([]int, len(b.kids))
	y := 0
	for i, k := range b.kids {
		_, heights[i] = s.measure(k, inner.Dx())
		tops[i] = y
		y += heights[i] + b.gap
	}
	if w.cursor < len(b.kids) {
		top, bottom := tops[w.cursor], tops[w.cursor]+heights[w.cursor]
		if top < w.offset {
			w.offset = top
		}
		if bottom > w.offset+in.Dy() {
			w.offset = bottom - in.Dy()
		}
	}
	w.offset = clampInt(w.offset, 0, max(y-b.gap-in.Dy(), 0))
	s.view.Scroll[b.key] = w.offset
	for i, k := range b.kids {
		// The cursor row's ground is under its children: a child that
		// repaints alone fills with it.
		k.ground = b.ground
		if w.selectable && i == w.cursor && s.pal.grounded() {
			k.ground = RoleCursor
		}
		s.place(k, rect(inner.Min.X, inner.Min.Y+tops[i]-w.offset, inner.Dx(), heights[i]))
	}
}

func (w *listWidget) paint(s *Surface, b *box, c *canvas, r Rect) {
	if !w.selectable || w.cursor >= len(b.kids) {
		return
	}
	g := b.kids[w.cursor]
	row := rect(r.Min.X, g.rect.Min.Y, r.Dx(), g.rect.Dy())
	cursorGround(s, c, row)
	for y := row.Min.Y; y < row.Max.Y; y++ {
		c.text(r.Min.X, y, s.g.cursor, style{fg: "accent"}, 1)
	}
}

// cursorGround paints the cursor row: the cursor ground, or at 16 colours
// and in ASCII nothing, since the text goes reverse instead.
func cursorGround(s *Surface, c *canvas, r Rect) {
	if s.pal.grounded() {
		c.fill(r, RoleCursor)
	}
}

// scrollWidget scrolls its children in its height, with a bar at the right
// when they do not fit.
type scrollWidget struct {
	content int // the children's height
	over    bool
}

func (w *scrollWidget) measure(s *Surface, b *box, maxW int) (int, int) {
	return s.measureCol(b.kids, maxW, b.gap)
}

func (w *scrollWidget) place(s *Surface, b *box, in Rect) {
	_, ch := s.measureCol(b.kids, in.Dx(), b.gap)
	w.over = ch > in.Dy()
	width := in.Dx()
	if w.over {
		width--
		_, ch = s.measureCol(b.kids, width, b.gap)
	}
	w.content = ch
	limit := max(ch-in.Dy(), 0)
	off, ok := lookup(s.view.Scroll, b)
	if !ok && b.src.AttrString("follow") == "end" || ok && off < 0 {
		off = limit
	}
	off = clampInt(off, 0, limit)
	mark := len(s.focus)
	s.placeCol(b, rect(in.Min.X, in.Min.Y-off, width, ch))
	// After tab, bring the focused box into view, and place again at that
	// offset.
	if f := focusedIn(s, b); f != nil {
		want := off
		switch {
		case f.rect.Min.Y < in.Min.Y:
			want = off - (in.Min.Y - f.rect.Min.Y)
		case f.rect.Max.Y > in.Max.Y:
			want = off + min(f.rect.Max.Y-in.Max.Y, f.rect.Min.Y-in.Min.Y)
		}
		want = clampInt(want, 0, limit)
		if want != off {
			off = want
			s.focus = s.focus[:mark]
			s.placeCol(b, rect(in.Min.X, in.Min.Y-off, width, ch))
		}
	}
	if b.src.AttrString("follow") == "end" && ok && off >= limit {
		// Keep following the end as content grows.
		s.view.Scroll[b.key] = -1
	} else {
		s.view.Scroll[b.key] = off
	}
}

// focusedIn returns the focused box when tab just moved the focus to a box
// inside b, or nil.
func focusedIn(s *Surface, b *box) *box {
	if s.view.Focus == "" || !s.focusMoved {
		return nil
	}
	var found *box
	var walk func(x *box)
	walk = func(x *box) {
		if found != nil {
			return
		}
		if x != b && x.key == s.view.Focus {
			found = x
			return
		}
		for _, k := range x.kids {
			walk(k)
		}
	}
	walk(b)
	return found
}

func (w *scrollWidget) offset(s *Surface, b *box) int {
	off, _ := lookup(s.view.Scroll, b)
	if off < 0 {
		return max(w.content-b.inner().Dy(), 0)
	}
	return off
}

func (w *scrollWidget) paint(s *Surface, b *box, c *canvas, r Rect) {
	if !w.over || r.Dy() <= 0 {
		return
	}
	x := r.Max.X - 1
	thumb := max(1, r.Dy()*r.Dy()/max(w.content, 1))
	top := 0
	if w.content > r.Dy() {
		top = (r.Dy() - thumb) * w.offset(s, b) / (w.content - r.Dy())
	}
	for y := 0; y < r.Dy(); y++ {
		g, st := s.g.scrollTrack, style{fg: "edge"}
		if y >= top && y < top+thumb {
			g, st = s.g.scrollTh, style{fg: "mute"}
		}
		c.text(x, r.Min.Y+y, g, st, 1)
	}
}

// viewport is where the children of a box may draw: its content area.
func viewport(b *box) Rect {
	in := b.inner()
	switch w := b.w.(type) {
	case *scrollWidget:
		if w.over {
			in.Max.X--
		}
	case *cardWidget:
		in = w.body(in).Intersect(in)
	case *tabsWidget:
		in.Min.Y = min(in.Min.Y+3, in.Max.Y)
	case *listWidget:
		in.Min.X = min(in.Min.X+w.indent(), in.Max.X)
	}
	return in
}

// scrolls reports whether b moves its children past its own edges, so
// their rectangles may lie outside it.
func scrolls(b *box) bool {
	switch b.w.(type) {
	case *scrollWidget, *listWidget:
		return true
	}
	return false
}

// needsPaint reports whether b or a box inside it changed since the last
// paint.
func needsPaint(b *box) bool {
	if b.dirty || b.rect != b.prev {
		return true
	}
	for _, k := range b.kids {
		if needsPaint(k) {
			return true
		}
	}
	return false
}

// paint draws b when it changed or force is set, and otherwise looks for
// changed boxes inside it. A box whose children moved repaints whole, as
// does a stack with any change, since its children overlap.
func (s *Surface) paint(b *box, c *canvas, force bool) {
	changed := force || b.dirty || b.rect != b.prev
	if !changed {
		for _, k := range b.kids {
			if k.rect != k.prev || (b.kind == "stack" || b.kind == "scroll") && needsPaint(k) {
				changed = true
				break
			}
		}
	}
	if !changed {
		if cw, ok := b.w.(*cardWidget); ok && cw.folded {
			return
		}
		// Children draw inside their parent's viewport, as a full paint
		// clips them.
		kc := c.withClip(viewport(b))
		for _, k := range b.kids {
			s.paint(k, kc, false)
		}
		return
	}
	cc := c.withClip(b.rect)
	if !force {
		s.addDamage(b.rect.Intersect(c.clip))
		ground := s.ground()
		if b.parent != nil {
			ground = b.parent.ground
		}
		cc.fill(b.rect, ground)
	}
	s.paintBox(b, cc)
}

// paintBox draws b and all of its children, and marks them painted.
func (s *Surface) paintBox(b *box, c *canvas) {
	b.prev, b.dirty = b.rect, false
	if b.hidden {
		markPainted(b)
		return
	}
	if b.w != nil {
		b.w.paint(s, b, c, b.inner())
	}
	if cw, ok := b.w.(*cardWidget); ok && cw.folded {
		return
	}
	kc := c.withClip(viewport(b))
	for _, k := range b.kids {
		s.paintBox(k, kc.withClip(k.rect))
	}
}

func markPainted(b *box) {
	b.prev, b.dirty = b.rect, false
	for _, k := range b.kids {
		markPainted(k)
	}
}

// Frame draws the frame a host puts around a floating surface into buf: the
// ground, a rounded border, the title on the top edge and a label at its
// right, such as "from claude in api-auth". The document cannot draw over
// the frame, so a host uses it to name the owner. The surface draws inside
// r shrunk by one cell on each side.
func Frame(dst *Buffer, r Rect, title, label string, p *Palette, focused bool) {
	buf := dst.b
	if p == nil {
		p = DefaultPalette(TrueColor)
	}
	g := &unicodeGlyphs
	if p.Depth == ASCII {
		g = &asciiGlyphs
	}
	c := &canvas{buf: buf, clip: r.Intersect(buf.Bounds()), pal: p, g: g}
	c.fill(r, "panel")
	edge := "edge"
	if focused {
		edge = "accent"
	}
	s := &Surface{g: g, pal: p}
	s.frame(c, r, title, label, edge)
}
