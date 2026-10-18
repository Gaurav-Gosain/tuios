package tml

import (
	"fmt"
	"sort"
	"strings"
)

// Action is one call in an action attribute such as on-press.
type Action struct {
	Name string
	Args []*Expr
	Off  int // byte offset of the call in the attribute
}

// The actions, and where each runs.
var actionSpecs = map[string]struct {
	min, max int
	literal  int // how many leading arguments must be string literals
	local    bool
	doc      string
}{
	"set":    {2, 2, 1, true, "set('path', expr) writes document state."},
	"toggle": {1, 1, 1, true, "toggle('path') flips a boolean in document state."},
	"tab":    {1, 1, 1, true, "tab('id') opens a tab."},
	"sort":   {1, 2, 2, true, "sort('field') or sort('table', 'field') sorts a table."},
	"filter": {2, 2, 1, true, "filter('id', expr) filters a table or list."},
	"fold":   {1, 1, 1, true, "fold('id') folds or unfolds a card."},
	"focus":  {1, 1, 1, true, "focus('id') moves focus to a node."},
	"emit":   {1, 2, 1, false, "emit('name', expr) sends an event to the owner."},
	"close":  {0, 1, 0, false, "close(expr) closes the surface with a value."},
	"copy":   {1, 1, 0, false, "copy(expr) puts text on the clipboard."},
	"open":   {1, 1, 1, false, "open('file:path') opens a file in a pane."},
	"pane":   {1, 2, 1, false, "pane('verb', {...}) runs focus, new-window or send-text."},
}

// ActionNames lists the actions, sorted.
func ActionNames() []string {
	out := make([]string, 0, len(actionSpecs))
	for k := range actionSpecs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var paneVerbs = []string{"focus", "new-window", "send-text"}

// ParseActions parses an action attribute: calls separated by ;.
func ParseActions(s string) ([]Action, error) {
	var out []Action
	for _, seg := range splitTop(s, ';') {
		src := s[seg[0]:seg[1]]
		if strings.TrimSpace(src) == "" {
			continue
		}
		e, err := ParseExpr(src)
		if err != nil {
			if se, ok := err.(*SyntaxError); ok {
				return nil, &SyntaxError{Offset: seg[0] + se.Offset, Msg: se.Msg}
			}
			return nil, err
		}
		call, ok := e.root.(*callNode)
		if !ok {
			return nil, &SyntaxError{Offset: seg[0], Msg: fmt.Sprintf("%q is not an action call; write one such as emit('name')", strings.TrimSpace(src))}
		}
		spec, ok := actionSpecs[call.name]
		if !ok {
			msg := fmt.Sprintf("%s is not an action", call.name)
			return nil, &SyntaxError{Offset: seg[0] + call.off, Msg: msg, Suggest: suggest(call.name, ActionNames())}
		}
		if len(call.args) < spec.min || len(call.args) > spec.max {
			return nil, &SyntaxError{Offset: seg[0] + call.off, Msg: fmt.Sprintf("%s takes %s", call.name, argCount(spec.min, spec.max))}
		}
		a := Action{Name: call.name, Off: seg[0] + call.off}
		for i, arg := range call.args {
			if i < spec.literal {
				if _, ok := literalString(arg); !ok {
					return nil, &SyntaxError{Offset: seg[0] + call.off, Msg: fmt.Sprintf("argument %d of %s must be a quoted string", i+1, call.name)}
				}
			}
			sub := &Expr{src: src, root: arg}
			collectDeps(arg, &sub.deps)
			a.Args = append(a.Args, sub)
		}
		if err := checkActionArgs(a); err != nil {
			return nil, &SyntaxError{Offset: a.Off, Msg: err.Error()}
		}
		out = append(out, a)
	}
	return out, nil
}

func argCount(lo, hi int) string {
	if lo == hi {
		if lo == 1 {
			return "1 argument"
		}
		return fmt.Sprintf("%d arguments", lo)
	}
	return fmt.Sprintf("%d to %d arguments", lo, hi)
}

func literalString(n exprNode) (string, bool) {
	l, ok := n.(*litNode)
	if !ok {
		return "", false
	}
	s, ok := l.v.(string)
	return s, ok
}

// Literal returns the argument as a string literal.
func (a Action) Literal(i int) string {
	if i >= len(a.Args) {
		return ""
	}
	s, _ := literalString(a.Args[i].root)
	return s
}

func checkActionArgs(a Action) error {
	switch a.Name {
	case "set", "toggle":
		if _, err := PathPointer(a.Literal(0)); err != nil {
			return err
		}
	case "open":
		p := a.Literal(0)
		if !strings.HasPrefix(p, "file:") {
			return fmt.Errorf("open takes a file: path, such as open('file:notes.md')")
		}
		if err := checkFilePath(strings.TrimPrefix(p, "file:")); err != nil {
			return err
		}
	case "pane":
		if !contains(paneVerbs, a.Literal(0)) {
			return fmt.Errorf("pane takes one of %s", strings.Join(paneVerbs, ", "))
		}
	}
	return nil
}

// splitTop splits s at sep outside quotes and brackets, as byte ranges.
func splitTop(s string, sep byte) [][2]int {
	var out [][2]int
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case sep:
			if depth == 0 {
				out = append(out, [2]int{start, i})
				start = i + 1
			}
		}
	}
	return append(out, [2]int{start, len(s)})
}

// SortSpec is the sort of a table: a field, ascending unless Desc.
type SortSpec struct {
	Field string
	Desc  bool
}

// ParseSort parses field, field:asc or field:desc.
func ParseSort(s string) (SortSpec, bool) {
	f, dir, has := strings.Cut(s, ":")
	if f == "" || (has && dir != "asc" && dir != "desc") {
		return SortSpec{}, false
	}
	return SortSpec{Field: f, Desc: dir == "desc"}, true
}

// View is the view state of one client: the open tabs, table sorts, filters,
// folded cards and focus. Each client keeps its own, so a person and their
// phone can look at different tabs of one surface. Keys are node ids, or
// node JSON Pointers for nodes without one.
type View struct {
	Tabs   map[string]string
	Sort   map[string]SortSpec
	Filter map[string]string
	Fold   map[string]bool
	Focus  string
}

// NewView returns an empty view.
func NewView() *View {
	return &View{Tabs: map[string]string{}, Sort: map[string]SortSpec{}, Filter: map[string]string{}, Fold: map[string]bool{}}
}

// Effect is an action that leaves the document: the host carries it out.
type Effect struct {
	Action string // emit, close, copy, open, pane
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

// RunActions runs the actions of one attribute. source is the JSON Pointer
// of the node that holds the attribute; sort without a table id and tab use
// it to find their target. env supplies event, form and the other roots;
// RunActions adds state.
//
// Local actions apply in order, so set('a', 1); emit('x', state.a) emits 1.
// Effects for the owner and the host come back in Result in order.
func RunActions(doc *Node, store *Store, view *View, source string, actions []Action, env *Env) (Result, error) {
	var res Result
	if env == nil {
		env = &Env{}
	}
	vars := map[string]any{}
	for k, v := range env.Vars {
		vars[k] = v
	}
	evalArg := func(a Action, i int) (any, error) {
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
			ptr, _ := PathPointer(a.Literal(0))
			ch, err := store.Set(a.Literal(0), !Truthy(store.Get(ptr)))
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
			var table *Node
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
				cur, _ = ParseSort(table.AttrString("sort"))
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
			view.Filter[a.Literal(0)] = Stringify(v)
			res.View = true
		case "fold":
			id := a.Literal(0)
			cur, ok := view.Fold[id]
			if !ok {
				if n, _ := doc.Find(id); n != nil {
					v, _ := n.Attr("fold")
					cur = v.Kind == Bool && v.Bool
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
func nearest(doc *Node, ptr, typ string) (*Node, string) {
	for p := ptr; ; p = parentPointer(p) {
		if n := doc.At(p); n != nil && n.Type == typ {
			return n, p
		}
		if p == "" {
			break
		}
	}
	var found *Node
	var at string
	count := 0
	doc.Walk(func(n *Node, p string) bool {
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

func nodeKey(n *Node, ptr string) string {
	if id := n.ID(); id != "" {
		return id
	}
	return ptr
}

// SortRows returns rows sorted by spec, stably. Numbers sort by value and
// strings without regard to case. Rows that lack the field sort last.
func SortRows(rows []any, spec SortSpec) []any {
	out := append([]any(nil), rows...)
	fieldOf := func(r any) any {
		if m, ok := r.(map[string]any); ok {
			return normalize(m[spec.Field])
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
		c, ok := compare(a, b)
		if !ok {
			c = strings.Compare(Stringify(a), Stringify(b))
		}
		if spec.Desc {
			return c > 0
		}
		return c < 0
	})
	return out
}

// FilterRows returns the rows that contain text in any field, without regard
// to case. An empty text keeps every row.
func FilterRows(rows []any, text string) []any {
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
	return strings.Contains(strings.ToLower(Stringify(v)), text)
}
