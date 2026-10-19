package loom

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"tuios.dev/tml"
)

// depGraph records which nodes read which state paths, so a renderer recomputes
// only the nodes a patch touches. Build it once per tree with buildDeps.
// Nodes are named by their JSON Pointer in the tree. A text run counts
// toward the component that holds it.
type depGraph struct {
	nodes []nodeDeps
	clock []string
}

type nodeDeps struct {
	ptr   string
	paths []string // JSON Pointers into the state
}

// buildDeps walks a tree and records the state paths each node reads: its
// bindings, its data attributes, if and show. Inside each, list and a table,
// a path through the element name (item.x) counts as the whole array, since
// a change to any element can move the others.
func buildDeps(doc *tml.Node) *depGraph {
	d := &depGraph{}
	d.walk(doc, "", map[string]string{})
	return d
}

func (d *depGraph) walk(n *tml.Node, ptr string, scope map[string]string) {
	if n.IsText() {
		return
	}
	var paths []string
	clock := false
	// A form's fields are form.NAME: they read the paths the fields bind
	// and their values read.
	if n.Type == "form" {
		scope = withFormPaths(scope, formPaths(n, scope))
	}
	addExpr := func(e *tml.Expr) {
		for _, dep := range e.Deps() {
			if dep.Root == "form" {
				for _, p := range splitFormPaths(scope) {
					paths = appendUnique(paths, p)
				}
				continue
			}
			if p, ok := resolveDep(dep, scope); ok {
				paths = appendUnique(paths, p)
			}
		}
		if slices.Contains(e.Funcs(), "ago") {
			clock = true
		}
	}
	addTemplate := func(s string) {
		if t, err := tml.ParseTemplate(s); err == nil {
			for _, e := range t.Exprs() {
				addExpr(e)
			}
		}
	}

	// each binds its alias for the node itself, and list for its children.
	inner := scope
	if each, ok := n.Attr("each"); ok && each.Kind == tml.KindString {
		if base, ok := dataPointer(each.Str, scope); ok {
			paths = appendUnique(paths, base)
			inner = withAlias(scope, aliasOf(n), base)
		}
	}
	for _, a := range n.Attrs {
		if a.Name == "each" || a.Name == "state" {
			continue
		}
		typ, _ := tml.AttrTypeOf(n.Type, a.Name)
		switch typ {
		case tml.TAction, tml.TIdent, tml.TEnum, tml.TKey, tml.TPattern, tml.TField, tml.TSort, tml.THref, tml.TSrc, tml.TDuration, tml.TObject:
		case tml.TExpr:
			if e, err := tml.ParseBinding(a.Value.Text()); err == nil {
				addExpr(e)
			}
		case tml.TData, tml.TPath:
			if a.Value.Kind == tml.KindString {
				if p, ok := dataPointer(a.Value.Str, inner); ok {
					paths = appendUnique(paths, p)
				}
			}
		default:
			if a.Value.Kind == tml.KindString {
				addTemplate(a.Value.Str)
			}
		}
	}
	childScope := inner
	switch n.Type {
	case "list":
		if items, ok := n.Attr("items"); ok && items.Kind == tml.KindString {
			if base, ok := dataPointer(items.Str, inner); ok {
				childScope = withAlias(inner, n.AttrString("as"), base)
			}
		}
	case "table":
		if rows, ok := n.Attr("rows"); ok && rows.Kind == tml.KindString {
			if base, ok := dataPointer(rows.Str, inner); ok {
				childScope = withAlias(withAlias(inner, "row", base), "value", base)
			}
		}
	}
	for _, c := range n.Children {
		if c.IsText() && tml.ContentOf(n.Type) != tml.ContentRaw {
			addTemplate(c.Text)
		}
	}
	if len(paths) > 0 {
		sort.Strings(paths)
		d.nodes = append(d.nodes, nodeDeps{ptr, paths})
	}
	if clock {
		d.clock = append(d.clock, ptr)
	}
	for i, c := range n.Children {
		d.walk(c, ptr+"/children/"+strconv.Itoa(i), childScope)
	}
}

func aliasOf(n *tml.Node) string {
	if a := n.AttrString("as"); a != "" {
		return a
	}
	return "item"
}

func withAlias(scope map[string]string, alias, base string) map[string]string {
	if alias == "" {
		alias = "item"
	}
	out := make(map[string]string, len(scope)+1)
	for k, v := range scope {
		out[k] = v
	}
	out[alias] = base
	return out
}

// dataPointer resolves a data attribute's path to a state pointer.
func dataPointer(s string, scope map[string]string) (string, bool) {
	root, ptr, err := tml.SplitPath(s)
	if err != nil {
		return "", false
	}
	return resolveDep(tml.Dep{Root: root, Path: ptr}, scope)
}

func resolveDep(dep tml.Dep, scope map[string]string) (string, bool) {
	if dep.Root == "state" {
		return dep.Path, true
	}
	if base, ok := scope[dep.Root]; ok {
		return base, true
	}
	return "", false
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// Of returns the state pointers the node at ptr reads.
func (d *depGraph) Of(ptr string) []string {
	for _, n := range d.nodes {
		if n.ptr == ptr {
			return n.paths
		}
	}
	return nil
}

// Nodes returns the pointer of every node that reads state.
func (d *depGraph) Nodes() []string {
	out := make([]string, len(d.nodes))
	for i, n := range d.nodes {
		out[i] = n.ptr
	}
	return out
}

// Clock returns the nodes that call ago(), which need a minute tick while
// visible. When it is empty, the surface needs no timer for its bindings.
func (d *depGraph) Clock() []string { return d.clock }

// Affected returns the nodes that read any of the changed state pointers, in
// tree order. A change affects a node when one path is the other or lies
// below it: a change to /failures/0/ms affects a node that reads /failures,
// and a change to /failures affects one that reads /failures/0/ms.
func (d *depGraph) Affected(changed []string) []string {
	var out []string
	for _, n := range d.nodes {
	match:
		for _, p := range n.paths {
			for _, c := range changed {
				if overlaps(p, c) {
					out = append(out, n.ptr)
					break match
				}
			}
		}
	}
	return out
}

func overlaps(a, b string) bool {
	if a == b || a == "" || b == "" {
		return true
	}
	return strings.HasPrefix(b, a+"/") || strings.HasPrefix(a, b+"/")
}

// formKey holds a form's field paths in the scope, joined by newlines. No
// alias can have this name: a newline is not a name character.
const formKey = "\nform"

func withFormPaths(scope map[string]string, paths []string) map[string]string {
	out := make(map[string]string, len(scope)+1)
	for k, v := range scope {
		out[k] = v
	}
	out[formKey] = strings.Join(paths, "\n")
	return out
}

func splitFormPaths(scope map[string]string) []string {
	if v := scope[formKey]; v != "" {
		return strings.Split(v, "\n")
	}
	return nil
}

// formPaths returns the state paths a form's fields read: each bind path,
// and the paths their value bindings read.
func formPaths(form *tml.Node, scope map[string]string) []string {
	var out []string
	form.Walk(func(c *tml.Node, _ string) bool {
		if c.IsText() || c.AttrString("name") == "" {
			return true
		}
		if b := c.AttrString("bind"); b != "" {
			if p, ok := dataPointer(b, scope); ok {
				out = appendUnique(out, p)
			}
		}
		if v, ok := c.Attr("value"); ok && v.Kind == tml.KindString {
			if t, err := tml.ParseTemplate(v.Str); err == nil {
				for _, e := range t.Exprs() {
					for _, dep := range e.Deps() {
						if p, ok := resolveDep(dep, scope); ok {
							out = appendUnique(out, p)
						}
					}
				}
			}
		}
		return true
	})
	return out
}
