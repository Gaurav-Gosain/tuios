package main

import (
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"
	"tuios.dev/loom"
	"tuios.dev/tml"
)

// TML data and containers in guios (part 3): list, table, tree, tabs,
// modal, toast and split.
//
// Each keeps loom's view state in the surface's loom.View, under the
// element's id, or its pointer when it has none: the open tab, a table's
// sort and filter, the cursor row, the open nodes of a tree. A table's
// rows are sorted and filtered with loom's own SortRows and FilterRows, a
// column's format is evaluated with value and row as loom gives them, and
// on-select gets item and value, so a table orders and picks in guios as it
// does in the TUI.

// viewKey is the key of an element's view state.
func viewKey(n *tml.Node, ptr string) string {
	if id := n.ID(); id != "" {
		return id
	}
	return ptr
}

// selectRow runs an element's on-select for the item picked.
func (r *tmlDraw) selectRow(n *tml.Node, ptr string, vars map[string]any, item any) {
	r.run(r.actions(n, "on-select"), ptr, vars, map[string]any{"item": item, "value": item})
}

// data draws a data or container element of part 3.
func (r *tmlDraw) dataNode(n *tml.Node, ptr string, vars map[string]any) {
	switch n.Type {
	case "list":
		r.list(n, ptr, vars)
	case "table":
		r.table(n, ptr, vars)
	case "tree":
		r.tree(n, ptr, vars)
	case "tabs":
		r.tabs(n, ptr, vars)
	case "modal":
		r.modal(n, ptr, vars)
	case "toast":
		r.toastNode(n, ptr, vars)
	case "split":
		r.split(n, ptr, vars)
	}
}

// list repeats its children once per element of items, as item (or as),
// and, with select, picks a row.
func (r *tmlDraw) list(n *tml.Node, ptr string, vars map[string]any) {
	c, pal := r.c, r.pal
	items := r.data(n, "items", vars)
	alias := n.AttrString("as")
	if alias == "" {
		alias = "item"
	}
	sel := n.AttrString("select")
	selectable := sel == "single" || sel == "multi"
	key := viewKey(n, ptr)
	cur, _ := strconv.Atoi(r.v.view.Cursor[key])
	ui.Column(c.Key(ptr)).Gap(2).AlignItems(ui.Stretch).Children(func() {
		for i, it := range items {
			scope := tmlScope(vars, alias, it, i)
			row := ui.Column(c.Key(i)).AlignItems(ui.Stretch).Padding(4, 8).Radius(rRow)
			if selectable {
				switch {
				case i == cur:
					row.Background(pal.rowSelected)
				case row.Hovered():
					row.Background(pal.rowHover)
				}
				row.Cursor(ui.CursorPointer)
				if row.Clicked() {
					r.v.view.Cursor[key] = strconv.Itoa(i)
					c.Invalidate()
					r.selectRow(n, ptr, scope, it)
				}
			}
			row.Children(func() { r.children(n, ptr+"#"+strconv.Itoa(i), scope) })
		}
	})
}

// tmlScope is vars with one element of a repeat in scope.
func tmlScope(vars map[string]any, alias string, el any, i int) map[string]any {
	out := make(map[string]any, len(vars)+2)
	for k, x := range vars {
		out[k] = x
	}
	out[alias], out["index"] = el, float64(i)
	return out
}

// tmlColumn is a table column as drawn.
type tmlColumn struct {
	field, title, align string
	format              *tml.Expr
	sortable            bool
	width               float32
}

// table draws rows of a state array under sortable column headers.
func (r *tmlDraw) table(n *tml.Node, ptr string, vars map[string]any) {
	c, pal := r.c, r.pal
	key := viewKey(n, ptr)
	var cols []tmlColumn
	anySortable := false
	for _, col := range n.Children {
		if col.Type != "column" || !r.truthy(col, "if", vars) {
			continue
		}
		tc := tmlColumn{field: col.AttrString("field"), title: r.str(col, "title", vars), align: col.AttrString("align")}
		if tc.title == "" {
			tc.title = tc.field
		}
		if f := col.AttrString("format"); f != "" {
			tc.format = r.expression(f)
		}
		if w, err := strconv.ParseFloat(col.AttrString("w"), 64); err == nil {
			tc.width = float32(w) * tmlCellW
		}
		v, _ := col.Attr("sort")
		tc.sortable = v.Kind == tml.KindBool && v.Bool
		anySortable = anySortable || tc.sortable
		cols = append(cols, tc)
	}
	if !anySortable {
		for i := range cols {
			cols[i].sortable = true
		}
	}
	rows := r.data(n, "rows", vars)
	filter, ok := r.v.view.Filter[key]
	if !ok {
		filter = r.str(n, "filter", vars)
	}
	rows = loom.FilterRows(rows, filter)
	spec, ok := r.v.view.Sort[key]
	if !ok {
		spec, _ = loom.ParseSort(n.AttrString("sort"))
	}
	if spec.Field != "" {
		rows = loom.SortRows(rows, spec)
	}
	sel := n.AttrString("select")
	selectable := sel == "single" || sel == "multi"
	keyField := n.AttrString("key")
	rowKey := func(i int) string {
		if m, ok := rows[i].(map[string]any); ok && keyField != "" {
			if v, ok := m[keyField]; ok {
				return "k:" + tml.Stringify(v)
			}
		}
		return "i:" + strconv.Itoa(i)
	}
	cur := r.v.view.Cursor[key]
	cell := func(col tmlColumn, row any) string {
		m, _ := row.(map[string]any)
		v := m[col.field]
		if col.format == nil {
			return tml.Stringify(v)
		}
		env := tmlScope(vars, "row", row, 0)
		env["value"] = v
		out, err := col.format.Eval(&tml.Env{Vars: env})
		if err != nil {
			return "!"
		}
		return tml.Stringify(out)
	}
	cellBox := func(e ui.Element, col tmlColumn) ui.Element {
		if col.width > 0 {
			e = e.Width(col.width).Shrink(0)
		} else {
			e = e.Grow(1).Basis(0).MinWidth(0)
		}
		if col.align == "end" || col.align == "right" {
			e = e.TextAlign(ui.End)
		}
		return e
	}
	ui.Column(c.Key(ptr)).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Gap(tmlCellW*2).Padding(4, 8).Children(func() {
			for i, col := range cols {
				title := col.title
				if spec.Field == col.field {
					title += map[bool]string{false: " ↑", true: " ↓"}[spec.Desc]
				}
				h := cellBox(ui.Text(c.Key(i), title).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.textMuted).SingleLine().Ellipsis("…"), col)
				if col.sortable {
					h.Cursor(ui.CursorPointer)
					if h.Clicked() {
						next := loom.SortSpec{Field: col.field}
						if spec.Field == col.field && !spec.Desc {
							next.Desc = true
						}
						r.v.view.Sort[key] = next
						c.Invalidate()
					}
				}
			}
		})
		ui.Box(c).Height(1).Background(pal.divider)
		for i, row := range rows {
			line := ui.Row(c.Key(rowKey(i))).Gap(tmlCellW*2).Padding(4, 8).Radius(rRow)
			if selectable {
				switch {
				case rowKey(i) == cur:
					line.Background(pal.rowSelected)
				case line.Hovered():
					line.Background(pal.rowHover)
				}
				line.Cursor(ui.CursorPointer)
				if line.Clicked() {
					r.v.view.Cursor[key] = rowKey(i)
					c.Invalidate()
					r.selectRow(n, ptr, vars, row)
				}
			}
			line.Children(func() {
				for j, col := range cols {
					cellBox(ui.Text(c.Key(j), cell(col, row)).FontSize(fsBody).TextColor(pal.text).SingleLine().Ellipsis("…").FontFeatures("tnum"), col)
				}
			})
		}
	})
}

// expression is a parsed expression, parsed once per surface.
func (r *tmlDraw) expression(src string) *tml.Expr {
	if r.v.expr == nil {
		r.v.expr = map[string]*tml.Expr{}
	}
	e, ok := r.v.expr["x:"+src]
	if !ok {
		e, _ = tml.ParseExpr(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(src), "{"), "}"))
		r.v.expr["x:"+src] = e
	}
	return e
}

// tree draws nested state objects, each by its label field, with its
// children field folded open to open levels.
func (r *tmlDraw) tree(n *tml.Node, ptr string, vars map[string]any) {
	c, pal := r.c, r.pal
	key := viewKey(n, ptr)
	labelF, kidsF := n.AttrString("label"), n.AttrString("children")
	if labelF == "" {
		labelF = "label"
	}
	if kidsF == "" {
		kidsF = "children"
	}
	open := int(r.num(n, "open", vars, 1))
	selectable := n.AttrString("select") == "single" || n.AttrString("select") == "multi"
	cur := r.v.view.Cursor[key]
	var walk func(items []any, depth int, path string)
	walk = func(items []any, depth int, path string) {
		for i, it := range items {
			m, _ := it.(map[string]any)
			p := path + "/" + strconv.Itoa(i)
			kids, _ := m[kidsF].([]any)
			fk := key + p
			isOpen, set := r.v.view.Fold[fk]
			if !set {
				isOpen = depth < open
			}
			row := ui.Row(c.Key(p)).Gap(6).AlignItems(ui.Center).Padding(3, 8, 3, float32(8+depth*14)).Radius(rRow).Cursor(ui.CursorPointer)
			switch {
			case selectable && cur == p:
				row.Background(pal.rowSelected)
			case row.Hovered():
				row.Background(pal.rowHover)
			}
			if row.Clicked() {
				c.Invalidate()
				if len(kids) > 0 {
					r.v.view.Fold[fk] = !isOpen
				}
				if selectable {
					r.v.view.Cursor[key] = p
					r.selectRow(n, ptr, vars, it)
				}
			}
			row.Children(func() {
				mark := " "
				if len(kids) > 0 {
					mark = map[bool]string{false: "▸", true: "▾"}[isOpen]
				}
				ui.Text(c, mark).FontSize(fsSmall).TextColor(pal.textFaint).Width(10)
				ui.Text(c, tml.Stringify(m[labelF])).FontSize(fsBody).TextColor(pal.text)
			})
			if len(kids) > 0 && isOpen {
				walk(kids, depth+1, p)
			}
		}
	}
	ui.Column(c.Key(ptr)).AlignItems(ui.Stretch).Children(func() {
		walk(r.data(n, "items", vars), 0, "")
	})
}

// tabs draws its tabs' titles, and the open tab under them. The open tab is
// in the state at value, or in the view; on-change gets event.value.
func (r *tmlDraw) tabs(n *tml.Node, ptr string, vars map[string]any) {
	c, pal := r.c, r.pal
	key := viewKey(n, ptr)
	var ids, titles []string
	var tabs []*tml.Node
	var ptrs []string
	for i, t := range n.Children {
		if t.Type != "tab" || !r.truthy(t, "if", vars) {
			continue
		}
		tabs, ptrs = append(tabs, t), append(ptrs, ptr+"/children/"+strconv.Itoa(i))
		ids, titles = append(ids, t.ID()), append(titles, r.str(t, "title", vars))
	}
	if len(tabs) == 0 {
		return
	}
	statePath := n.AttrString("value")
	open := ""
	if statePath != "" {
		if p, err := tml.PathPointer(statePath); err == nil {
			open = tml.Stringify(r.v.store.Get(p))
		}
	} else {
		open = r.v.view.Tabs[key]
	}
	at := 0
	for i, id := range ids {
		if id == open {
			at = i
		}
	}
	ui.Column(c.Key(ptr)).Gap(tmlCellH / 2).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Gap(4).Children(func() {
			for i, title := range titles {
				t := ui.Text(c.Key(i), title).FontSize(fsBody).Padding(4, 10).Radius(rRow).Cursor(ui.CursorPointer)
				switch {
				case i == at:
					t.TextColor(pal.text).FontWeight(wSemibold).Background(pal.rowSelected)
				case t.Hovered():
					t.TextColor(pal.text).Background(pal.rowHover)
				default:
					t.TextColor(pal.textMuted)
				}
				if t.Clicked() && i != at {
					c.Invalidate()
					res := loom.Result{View: true}
					if statePath != "" {
						ch, err := r.v.store.Set(statePath, ids[i])
						if err == nil {
							res.Changed = ch
						}
					} else {
						r.v.view.Tabs[key] = ids[i]
					}
					if acts := r.actions(n, "on-change"); len(acts) > 0 {
						env := tmlScope(vars, "event", map[string]any{"value": ids[i]}, 0)
						r2, _ := loom.RunActions(r.v.doc, r.v.store, r.v.view, ptr, acts, &tml.Env{Vars: env})
						res.Changed = append(res.Changed, r2.Changed...)
						res.Effects = append(res.Effects, r2.Effects...)
					}
					r.a.tmlResult(r.v, res)
				}
			}
		})
		ui.Box(c).Height(1).Background(pal.divider)
		r.children(tabs[at], ptrs[at], vars)
	})
}

// modal draws its content in a raised card while open is true.
func (r *tmlDraw) modal(n *tml.Node, ptr string, vars map[string]any) {
	if v, ok := r.value(n, "open", vars); ok && !tml.Truthy(v) {
		return
	}
	c, pal := r.c, r.pal
	ui.Column(c.Key(ptr)).Gap(tmlCellH/2).Padding(14, 16).Radius(rControl).Background(pal.popover).
		Border(1, pal.cardBorder).AlignItems(ui.Stretch).Children(func() {
		if t := r.str(n, "title", vars); t != "" {
			ui.Text(c, t).FontSize(fsBody + 2).FontWeight(wSemibold).TextColor(pal.text)
		}
		r.children(n, ptr, vars)
	})
}

// toastNode draws a banner in its role's colour, gone after ttl from when
// guios first drew it. It asks for one frame at the end, and none before.
func (r *tmlDraw) toastNode(n *tml.Node, ptr string, vars map[string]any) {
	c, pal := r.c, r.pal
	key := viewKey(n, ptr)
	if ttl, err := time.ParseDuration(n.AttrString("ttl")); err == nil && ttl > 0 {
		if r.v.toasts == nil {
			r.v.toasts = map[string]time.Time{}
		}
		first, seen := r.v.toasts[key]
		if !seen {
			first = c.Now()
			r.v.toasts[key] = first
		}
		left := ttl - c.Now().Sub(first)
		if left <= 0 {
			return
		}
		c.After(left)
	}
	col := r.role(n.AttrString("role"), pal.accent)
	ui.Row(c.Key(ptr)).Gap(8).AlignItems(ui.Center).Padding(8, 12).Radius(rControl).Background(col.Alpha(0.14)).Children(func() {
		ui.Box(c).Size(6, 6).Radius(3).Background(col)
		ui.RichText(c, r.inline(n, vars, ui.Span{Color: pal.text})...).FontSize(fsBody).Grow(1).MinWidth(0)
	})
}

// split lays its two children side by side (dir h, the default) or one
// over the other (dir v), the first taking ratio of the room.
func (r *tmlDraw) split(n *tml.Node, ptr string, vars map[string]any) {
	c, pal := r.c, r.pal
	ratio := float32(min(max(r.num(n, "ratio", vars, 0.5), 0.1), 0.9))
	var kids []*tml.Node
	var ptrs []string
	for i, k := range n.Children {
		if !k.IsText() {
			kids, ptrs = append(kids, k), append(ptrs, ptr+"/children/"+strconv.Itoa(i))
		}
	}
	if len(kids) < 2 {
		return
	}
	vertical := n.AttrString("dir") == "v"
	var box ui.Element
	if vertical {
		box = ui.Column(c.Key(ptr)).AlignItems(ui.Stretch)
	} else {
		box = ui.Row(c.Key(ptr)).AlignItems(ui.Stretch)
	}
	box.Gap(tmlCellW).Children(func() {
		ui.Column(c.Key("a")).Grow(ratio).Basis(0).MinWidth(0).AlignItems(ui.Stretch).Children(func() { r.node(kids[0], ptrs[0], vars) })
		if vertical {
			ui.Box(c).Height(1).Background(pal.divider)
		} else {
			ui.Box(c).Width(1).Background(pal.divider)
		}
		ui.Column(c.Key("b")).Grow(1 - ratio).Basis(0).MinWidth(0).AlignItems(ui.Stretch).Children(func() { r.node(kids[1], ptrs[1], vars) })
	})
}
