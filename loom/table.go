package loom

import (
	"strconv"
	"strings"

	"tuios.dev/tml"
)

// tableWidget shows rows of a state array in columns. Sorting, filtering,
// the cursor and scrolling are view state and run locally. Only the rows in
// view are formatted, so a table of 50,000 rows costs what its height does.
type tableWidget struct {
	cols       []tableCol
	rows       []any // after the filter and the sort
	keyField   string
	sort       SortSpec
	selectable bool
	cursor     int
	offset     int
	onSelect   []tml.Action
	vars       map[string]any

	widths, xs []int // at the last place
	widthsFor  int   // the width the widths were shared for
	bodyTop    int   // the screen row of the first data row
}

type tableCol struct {
	node     *tml.Node
	field    string
	title    string
	align    textAlign
	size     size
	format   *tml.Expr
	sortable bool
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

// rowKey is the identity of a row: its key field, or its index.
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

// find returns the index of the row with the given identity, or 0.
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

// cell returns the text of one cell: the column's format of the value, or
// the value as text.
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

// columnWidths shares the width among the columns: fixed and percent
// widths, then auto columns at their widest text in the first rows, then
// fill weights.
func (s *Surface) columnWidths(w *tableWidget, total int) []int {
	// Sampling the rows has its own budget, so a wide table cannot starve
	// the rows on the screen.
	frame := s.budget
	s.budget = &tml.Budget{Steps: s.boxSteps}
	defer func() { s.budget = frame }()
	n := len(w.cols)
	avail := max(total-w.margin()-2*max(n-1, 0), 0)
	items := make([]item, n)
	for i := range w.cols {
		col := &w.cols[i]
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
	return distribute(avail, items)
}

func (w *tableWidget) place(s *Surface, b *box, in Rect) {
	// The widths depend only on the rows and the width, and the widget is
	// built again when its rows change.
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
	// The rows in view format on a budget of their own, the same at every
	// paint.
	frame := s.budget
	s.budget = &tml.Budget{Steps: s.boxSteps}
	defer func() { s.budget = frame }()
	g := s.g
	for i, col := range w.cols {
		t := strings.ToUpper(col.title)
		st := style{fg: "mute"}
		l := line{{text: t, st: st}}
		if col.field == w.sort.Field && w.sort.Field != "" {
			l[0].st = st.with(attrBold)
			arrow := g.sortUp
			if w.sort.Desc {
				arrow = g.sortDown
			}
			l = append(l, span{text: " " + arrow, st: style{fg: "accent-bright"}})
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
			c.drawLine(w.xs[i], y, w.widths[i], plainLine(s.tableCell(w, col, w.rows[ri]), st), col.align)
		}
	}
}

// colAt returns the column under screen column x, or -1.
func (w *tableWidget) colAt(x int) int {
	for i, cx := range w.xs {
		if x >= cx && x < cx+w.widths[i]+2 {
			return i
		}
	}
	return -1
}
