package loom

import (
	"strconv"
	"strings"
	"unicode"

	"tuios.dev/tml"
)

type tableWidget struct {
	cols       []tableCol
	rows       []any
	keyField   string
	sort       SortSpec
	selectable bool
	cursor     int
	offset     int
	onSelect   []tml.Action
	vars       map[string]any

	widths, xs []int
	widthsFor  int
	bodyTop    int
}

type tableCol struct {
	node     *tml.Node
	field    string
	title    string
	align    textAlign
	size     size
	format   *tml.Expr
	sortable bool
	path     bool
	tailW    int
}

func (s *Surface) buildTable(b *box, vars map[string]any) widget {
	n := b.src
	w := &tableWidget{keyField: n.AttrString("key"), vars: vars, onSelect: s.actionsOf(n, "on-select")}
	sel := n.AttrString("select")
	w.selectable = sel == "single" || sel == "multi"
	anySortable := false
	for _, c := range n.Children {
		if c.Type != "column" || !s.truthy(c, "if", vars) {
			continue
		}
		col := tableCol{node: c, field: c.AttrString("field"), title: s.str(c, "title", vars),
			align: alignOf(c.AttrString("align")), size: parseSize(c.AttrString("w"))}
		if col.title == "" {
			col.title = col.field
		}
		if f := c.AttrString("format"); f != "" {
			col.format = s.expr(c, "format", f)
		}
		v, _ := c.Attr("sort")
		col.sortable = v.Kind == tml.KindBool && v.Bool
		anySortable = anySortable || col.sortable
		w.cols = append(w.cols, col)
	}
	if !anySortable {
		for i := range w.cols {
			w.cols[i].sortable = true
		}
	}
	rows := s.data(n, "rows", vars)
	filter, ok := lookup(s.view.Filter, b)
	if !ok {
		filter = s.str(n, "filter", vars)
	}
	rows = filterRows(rows, filter)
	if spec, ok := lookup(s.view.Sort, b); ok {
		w.sort = spec
	} else {
		w.sort, _ = parseSort(n.AttrString("sort"))
	}
	if w.sort.Field != "" {
		rows = sortRows(rows, w.sort)
	}
	w.rows = rows
	if cur, ok := lookup(s.view.Cursor, b); ok {
		w.cursor = w.find(cur)
	}
	w.offset, _ = lookup(s.view.Scroll, b)
	return w
}

func (w *tableWidget) rowKey(i int) string {
	if w.keyField != "" && i < len(w.rows) {
		if m, ok := w.rows[i].(map[string]any); ok {
			if v, ok := m[w.keyField]; ok {
				return "k:" + tml.Stringify(v)
			}
		}
	}
	return "i:" + strconv.Itoa(i)
}

func (w *tableWidget) find(key string) int {
	if strings.HasPrefix(key, "i:") {
		i, _ := strconv.Atoi(key[2:])
		return clampInt(i, 0, max(len(w.rows)-1, 0))
	}
	for i := range w.rows {
		if w.rowKey(i) == key {
			return i
		}
	}
	return 0
}

func (w *tableWidget) measure(_ *Surface, _ *box, maxW int) (int, int) {
	return maxW, 1 + len(w.rows)
}

func (w *tableWidget) margin() int {
	if w.selectable {
		return 2
	}
	return 0
}

func (s *Surface) tableCell(w *tableWidget, col *tableCol, row any) string {
	m, _ := row.(map[string]any)
	v := m[col.field]
	if col.format == nil {
		return tml.Stringify(v)
	}
	w.vars["value"] = v
	w.vars["row"] = row
	out, err := col.format.Eval(s.env(w.vars))
	if err != nil {
		return "!"
	}
	return tml.Stringify(out)
}

func (s *Surface) columnWidths(w *tableWidget, total int) []int {
	frame := s.budget
	s.budget = &tml.Budget{Steps: s.boxSteps}
	defer func() { s.budget = frame }()
	n := len(w.cols)
	avail := max(total-w.margin()-2*max(n-1, 0), 0)
	items := make([]item, n)
	natural := make([]int, n)
	tail := make([]int, n)
	for i := range w.cols {
		col := &w.cols[i]
		natural[i] = width(col.title)
		if col.field == w.sort.Field && w.sort.Field != "" {
			natural[i] += 2
		}
		slash, spaced := false, false
		for r := 0; r < min(len(w.rows), 100); r++ {
			text := s.tableCell(w, col, w.rows[r])
			natural[i] = max(natural[i], width(text))
			slash = slash || isPathLike(text)
			spaced = spaced || strings.Contains(text, " ")
			tail[i] = max(tail[i], width(pathTail(text)))
		}
		col.path = slash && !spaced
		col.tailW = tail[i]
		it := item{hi: -1}
		switch fixed := col.size.resolve(avail); {
		case fixed >= 0:
			it.base = fixed
			it.shrink = true
			it.lo = min(fixed, 4)
		case col.size.kind == sizeFill:
			it.weight = col.size.n
			it.base = width(col.title)
			it.lo = min(width(col.title), 4)
			it.shrink = true
		default:
			it.base = width(col.title) + 2
			for r := 0; r < min(len(w.rows), 100); r++ {
				it.base = max(it.base, width(s.tableCell(w, col, w.rows[r])))
			}
			it.shrink = true
			it.lo = min(it.base, 4)
		}
		items[i] = it
	}
	sum := func() int {
		t := 0
		for _, it := range items {
			t += clampSize(it.base, it.lo, it.hi)
		}
		return t
	}
	for i := range items {
		if over := sum() - avail; over > 0 && items[i].base > natural[i] {
			items[i].base -= min(over, items[i].base-natural[i])
		}
	}
	for i, col := range w.cols {
		if over := sum() - avail; over > 0 && col.path {
			floor := max(min(tail[i]-2, items[i].base), items[i].lo)
			items[i].base -= min(over, max(items[i].base-floor, 0))
			items[i].lo = floor
		}
	}
	return distribute(avail, items)
}

func isPathLike(text string) bool {
	if !strings.Contains(text, "/") {
		return false
	}
	for seg := range strings.SplitSeq(text, "/") {
		for _, r := range seg {
			if unicode.IsLetter(r) {
				return true
			}
		}
	}
	return false
}

func pathTail(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return p
	}
	return "…" + p[i:]
}

func cutPath(p string, n int, ellipsis string) string {
	if width(p) <= n {
		return p
	}
	parts := strings.Split(p, "/")
	out := parts[len(parts)-1]
	for i := len(parts) - 2; i >= 0; i-- {
		next := parts[i] + "/" + out
		if width(ellipsis)+1+width(next) > n {
			break
		}
		out = next
	}
	if t := ellipsis + "/" + out; width(t) <= n {
		return t
	}
	return truncate(out, n, ellipsis)
}

func (w *tableWidget) place(s *Surface, b *box, in Rect) {
	if w.widths == nil || w.widthsFor != in.Dx() {
		w.widths = s.columnWidths(w, in.Dx())
		w.widthsFor = in.Dx()
	}
	w.xs = make([]int, len(w.widths))
	x := in.Min.X + w.margin()
	for i, wd := range w.widths {
		w.xs[i] = x
		x += wd + 2
	}
	w.bodyTop = in.Min.Y + 1
	visible := max(in.Dy()-1, 0)
	w.cursor = clampInt(w.cursor, 0, max(len(w.rows)-1, 0))
	if w.cursor < w.offset {
		w.offset = w.cursor
	}
	if visible > 0 && w.cursor >= w.offset+visible {
		w.offset = w.cursor - visible + 1
	}
	w.offset = clampInt(w.offset, 0, max(len(w.rows)-visible, 0))
	s.view.Scroll[b.key] = w.offset
}

func (w *tableWidget) paint(s *Surface, _ *box, c *canvas, r Rect) {
	if len(w.widths) != len(w.cols) || r.Dy() <= 0 {
		return
	}
	frame := s.budget
	s.budget = &tml.Budget{Steps: s.boxSteps}
	defer func() { s.budget = frame }()
	g := s.g
	for i, col := range w.cols {
		t := strings.ToUpper(col.title)
		st := style{fg: "mute"}
		l := line{{text: t, st: st}}
		if col.field == w.sort.Field && w.sort.Field != "" {
			arrow := g.sortUp
			if w.sort.Desc {
				arrow = g.sortDown
			}
			mark := " " + arrow
			if room := w.widths[i] - width(mark); width(t) > room {
				t = truncate(t, max(room, 0), g.ellipsis)
			}
			l = line{{text: t, st: style{fg: "dim", attrs: attrBold}}, {text: mark, st: style{fg: "accent-bright"}}}
		}
		c.drawLine(w.xs[i], r.Min.Y, w.widths[i], l, col.align)
	}
	grounded := s.pal.grounded()
	for vi := 0; vi < r.Dy()-1; vi++ {
		ri := w.offset + vi
		if ri >= len(w.rows) {
			break
		}
		y := r.Min.Y + 1 + vi
		cur := w.selectable && ri == w.cursor
		if cur {
			row := rect(r.Min.X, y, r.Dx(), 1)
			if grounded {
				c.fill(row, RoleCursor)
			} else {
				c.text(r.Min.X+1, y, strings.Repeat(" ", max(r.Dx()-1, 0)), style{fg: "dim", attrs: attrReverse}, -1)
			}
			c.text(r.Min.X, y, g.cursor, style{fg: "accent"}, 1)
		}
		for i := range w.cols {
			col := &w.cols[i]
			st := style{fg: "fg"}
			if i == 0 && len(w.cols) > 1 {
				st.fg = "dim"
			}
			if cur {
				if i > 0 {
					st = st.with(attrBold)
				}
				if !grounded {
					st = style{fg: "dim", attrs: attrReverse | st.attrs}
				}
			}
			text := s.tableCell(w, col, w.rows[ri])
			if col.path && width(text) > w.widths[i] {
				if w.widths[i] >= col.tailW {
					text = cutPath(text, w.widths[i], g.ellipsis)
				} else {
					text = truncate(text[strings.LastIndex(text, "/")+1:], w.widths[i], g.ellipsis)
				}
			}
			c.drawLine(w.xs[i], y, w.widths[i], plainLine(text, st), col.align)
		}
	}
}

func (w *tableWidget) colAt(x int) int {
	for i, cx := range w.xs {
		if x >= cx && x < cx+w.widths[i]+2 {
			return i
		}
	}
	return -1
}
