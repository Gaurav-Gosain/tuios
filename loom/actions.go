package loom

import (
	"fmt"
	"maps"
	"sort"
	"strings"

	"tuios.dev/tml"
)

// SortSpec is the sort of a table: a field, ascending unless Desc.
type SortSpec struct {
	Field string
	Desc  bool
}

// parseSort parses field, field:asc or field:desc.
func parseSort(s string) (SortSpec, bool) {
	f, dir, has := strings.Cut(s, ":")
	if f == "" || (has && dir != "asc" && dir != "desc") {
		return SortSpec{}, false
	}
	return SortSpec{Field: f, Desc: dir == "desc"}, true
}

// View is the view state of one client: the open tabs, table sorts,
// filters, folded cards, focus, cursor rows, scroll offsets and split
// ratios. Each client keeps its own, so a person and their phone can look at
// different tabs of one surface. Keys are node ids, or node JSON Pointers
// for nodes without one.
type View struct {
	Tabs   map[string]string
	Sort   map[string]SortSpec
	Filter map[string]string
	Fold   map[string]bool
	Focus  string
	// Cursor holds the cursor row of a table or list: the row's key field,
	// or its index when the node names no key.
	Cursor map[string]string
	Scroll map[string]int
	Split  map[string]float64
	// Text holds what was typed into a text field that binds no state
	// path, and Caret the caret of each text field, in runes.
	Text  map[string]string
	Caret map[string]int
}

// newView returns an empty view.
func newView() *View {
	return &View{Tabs: map[string]string{}, Sort: map[string]SortSpec{}, Filter: map[string]string{},
		Fold: map[string]bool{}, Cursor: map[string]string{}, Scroll: map[string]int{}, Split: map[string]float64{},
		Text: map[string]string{}, Caret: map[string]int{}}
}

// Effect is an action that leaves the document: the host carries it out.
type Effect struct {
	Action string // emit, close, copy, open, pane, or link (a click on a link, for the host to confirm)
	Name   string // emit: the event name; open: the path; pane: the verb
	Value  any
	// HasValue tells close(null) from close().
	HasValue bool
}

// Result is what running actions did.
type Result struct {
	Changed []string // JSON Pointers of the state that changed
	View    bool     // the view changed
	Effects []Effect
}

// runActions runs the actions of one attribute. source is the JSON Pointer
// of the node that holds the attribute; sort without a table id and tab use
// it to find their target. env supplies event, form and the other roots;
// runActions adds state.
//
// Local actions apply in order, so set('a', 1); emit('x', state.a) emits 1.
// Effects for the owner and the host come back in Result in order.
func runActions(doc *tml.Node, store *tml.Store, view *View, source string, actions []tml.Action, env *tml.Env) (Result, error) {
	var res Result
	if env == nil {
		env = &tml.Env{}
	}
	if env.Budget == nil {
		e := *env
		e.Budget = tml.NewBudget()
		env = &e
	}
	vars := map[string]any{}
	maps.Copy(vars, env.Vars)
	evalArg := func(a tml.Action, i int) (any, error) {
		vars["state"] = store.State()
		e := *env
		e.Vars = vars
		return a.Args[i].Eval(&e)
	}
	for _, a := range actions {
		switch a.Name {
		case "set":
			v, err := evalArg(a, 1)
			if err != nil {
				return res, err
			}
			ch, err := store.Set(a.Literal(0), v)
			if err != nil {
				return res, err
			}
			res.Changed = append(res.Changed, ch...)
		case "toggle":
			ptr, _ := tml.PathPointer(a.Literal(0))
			ch, err := store.Set(a.Literal(0), !tml.Truthy(store.Get(ptr)))
			if err != nil {
				return res, err
			}
			res.Changed = append(res.Changed, ch...)
		case "tab":
			id := a.Literal(0)
			tab, ptr := doc.Find(id)
			if tab == nil || tab.Type != "tab" {
				return res, fmt.Errorf("tab(%q): no tab has that id", id)
			}
			tabs := doc.At(parentPointer(ptr))
			if tabs == nil {
				return res, fmt.Errorf("tab(%q): the tab is not in tabs", id)
			}
			if p := tabs.AttrString("value"); p != "" {
				ch, err := store.Set(p, id)
				if err != nil {
					return res, err
				}
				res.Changed = append(res.Changed, ch...)
			} else {
				view.Tabs[nodeKey(tabs, parentPointer(ptr))] = id
				res.View = true
			}
		case "sort":
			field := a.Literal(0)
			var table *tml.Node
			var tptr string
			if len(a.Args) == 2 {
				table, tptr = doc.Find(field)
				field = a.Literal(1)
			} else {
				table, tptr = nearest(doc, source, "table")
			}
			if table == nil || table.Type != "table" {
				return res, fmt.Errorf("sort(%q): no table to sort", field)
			}
			key := nodeKey(table, tptr)
			cur, ok := view.Sort[key]
			if !ok {
				cur, _ = parseSort(table.AttrString("sort"))
			}
			if cur.Field == field {
				cur.Desc = !cur.Desc
			} else {
				cur = SortSpec{Field: field}
			}
			view.Sort[key] = cur
			res.View = true
		case "filter":
			v, err := evalArg(a, 1)
			if err != nil {
				return res, err
			}
			view.Filter[a.Literal(0)] = tml.Stringify(v)
			res.View = true
		case "fold":
			id := a.Literal(0)
			cur, ok := view.Fold[id]
			if !ok {
				if n, _ := doc.Find(id); n != nil {
					v, _ := n.Attr("fold")
					cur = v.Kind == tml.KindBool && v.Bool
				}
			}
			view.Fold[id] = !cur
			res.View = true
		case "focus":
			view.Focus = a.Literal(0)
			res.View = true
		default:
			eff := Effect{Action: a.Name}
			valueArg := 0
			switch a.Name {
			case "emit", "open", "pane":
				eff.Name = a.Literal(0)
				valueArg = 1
			}
			if a.Name == "open" {
				eff.Name = strings.TrimPrefix(eff.Name, "file:")
			}
			if valueArg < len(a.Args) {
				v, err := evalArg(a, valueArg)
				if err != nil {
					return res, err
				}
				eff.Value, eff.HasValue = v, true
			}
			res.Effects = append(res.Effects, eff)
		}
	}
	return res, nil
}

func parentPointer(ptr string) string {
	i := strings.LastIndex(ptr, "/children/")
	if i < 0 {
		return ""
	}
	return ptr[:i]
}

// nearest returns the closest node of type typ at or above the node at ptr,
// or the only one in the document.
func nearest(doc *tml.Node, ptr, typ string) (*tml.Node, string) {
	for p := ptr; ; p = parentPointer(p) {
		if n := doc.At(p); n != nil && n.Type == typ {
			return n, p
		}
		if p == "" {
			break
		}
	}
	var found *tml.Node
	var at string
	count := 0
	doc.Walk(func(n *tml.Node, p string) bool {
		if n.Type == typ {
			found, at = n, p
			count++
		}
		return true
	})
	if count == 1 {
		return found, at
	}
	return nil, ""
}

func nodeKey(n *tml.Node, ptr string) string {
	if id := n.ID(); id != "" {
		return id
	}
	return ptr
}

// sortRows returns rows sorted by spec, stably. Numbers sort by value and
// strings without regard to case. Rows that lack the field sort last.
func sortRows(rows []any, spec SortSpec) []any {
	out := append([]any(nil), rows...)
	fieldOf := func(r any) any {
		if m, ok := r.(map[string]any); ok {
			return m[spec.Field]
		}
		return nil
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := fieldOf(out[i]), fieldOf(out[j])
		if a == nil || b == nil {
			return a != nil && b == nil
		}
		if as, ok := a.(string); ok {
			a = strings.ToLower(as)
		}
		if bs, ok := b.(string); ok {
			b = strings.ToLower(bs)
		}
		c := compare(a, b)
		if spec.Desc {
			return c > 0
		}
		return c < 0
	})
	return out
}

// filterRows returns the rows that contain text in any field, without regard
// to case. An empty text keeps every row.
func filterRows(rows []any, text string) []any {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return rows
	}
	var out []any
	for _, r := range rows {
		if rowContains(r, text) {
			out = append(out, r)
		}
	}
	return out
}

func rowContains(v any, text string) bool {
	switch x := v.(type) {
	case map[string]any:
		for _, e := range x {
			if rowContains(e, text) {
				return true
			}
		}
		return false
	case []any:
		for _, e := range x {
			if rowContains(e, text) {
				return true
			}
		}
		return false
	}
	return strings.Contains(strings.ToLower(tml.Stringify(v)), text)
}

func compare(a, b any) int {
	x, xok := a.(float64)
	y, yok := b.(float64)
	if xok && yok {
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	}
	return strings.Compare(tml.Stringify(a), tml.Stringify(b))
}
