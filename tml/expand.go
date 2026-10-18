package tml

import (
	"encoding/json"
	"fmt"
)

// maxMult caps the product of nested each counts.
const maxMult = 1 << 30

// expansion is what a tree turns into against a state: the nodes each and
// list repeat, the images after repetition, and the rows and data points of
// tables, lists, trees and charts after repetition.
type expansion struct {
	nodes, images, rows int
}

// mult multiplies two counts, capped.
func mult(a, b int) int {
	if a == 0 || b == 0 {
		return 0
	}
	if a > maxMult/b {
		return maxMult
	}
	return a * b
}

func addCap(a, b int) int { return min(a+b, maxMult) }

// measure walks a tree against a state and counts what it expands to. A
// count that cannot be known (no state, or a path that is not there) is 1.
func measure(doc *Node, state map[string]any) expansion {
	var e expansion
	if doc != nil {
		measureNode(doc, state, 1, nil, &e)
	}
	return e
}

func measureNode(n *Node, state map[string]any, times int, elems map[string][]any, e *expansion) {
	if n.IsText() {
		return
	}
	if each, ok := n.Attr("each"); ok {
		count, es := elementsOf(each, state, elems)
		times = mult(times, count)
		elems = withElems(elems, aliasOf(n), es)
	}
	if times > 1 {
		e.nodes = addCap(e.nodes, times)
	}
	childTimes := times
	switch n.Type {
	case "image":
		e.images = addCap(e.images, times)
	case "list":
		if items, ok := n.Attr("items"); ok {
			count, es := elementsOf(items, state, elems)
			childTimes = mult(times, count)
			e.rows = addCap(e.rows, mult(times, count))
			alias := n.AttrString("as")
			if alias == "" {
				alias = "item"
			}
			elems = withElems(elems, alias, es)
		}
	default:
		for _, name := range [...]string{"rows", "items", "data", "values"} {
			if data, ok := n.Attr(name); ok && dataAttr(n.Type, name) {
				count, _ := elementsOf(data, state, elems)
				e.rows = addCap(e.rows, mult(times, count))
			}
		}
	}
	for _, c := range n.Children {
		measureNode(c, state, childTimes, elems, e)
	}
}

// dataAttr reports whether a component's attribute is a data attribute.
func dataAttr(component, attr string) bool {
	t, ok := AttrTypeOf(component, attr)
	return ok && t == TData
}

func withElems(elems map[string][]any, name string, es []any) map[string][]any {
	out := make(map[string][]any, len(elems)+1)
	for k, v := range elems {
		out[k] = v
	}
	out[name] = es
	return out
}

func aliasOf(n *Node) string {
	if a := n.AttrString("as"); a != "" {
		return a
	}
	return "item"
}

// elementsOf returns how many times a data attribute repeats, and the
// elements, when they are known. A literal array counts its elements. A
// path into the state counts the array there. A path through an element
// name (item.kids) counts the longest such array across the elements.
func elementsOf(data Value, state map[string]any, elems map[string][]any) (int, []any) {
	if data.Kind == KindArray {
		var arr []any
		if json.Unmarshal(data.Raw, &arr) != nil {
			return 1, nil
		}
		return len(arr), arr
	}
	if data.Kind != KindString {
		return 1, nil
	}
	root, ptr, err := SplitPath(data.Str)
	if err != nil {
		return 1, nil
	}
	toks, _ := splitPointer(ptr)
	if root == "state" {
		if state == nil {
			return 1, nil
		}
		if arr, err := get(state, toks); err == nil {
			if a, ok := arr.([]any); ok {
				return len(a), a
			}
		}
		return 1, nil
	}
	parents, ok := elems[root]
	if !ok || parents == nil {
		return 1, nil
	}
	longest := 0
	var all []any
	for _, p := range parents {
		if arr, err := get(p, toks); err == nil {
			if a, ok := arr.([]any); ok {
				longest = max(longest, len(a))
				if len(all) < maxMult {
					all = append(all, a...)
				}
			}
		}
	}
	return max(longest, 1), all
}

// check returns the limit problems of an expansion, empty when it fits.
func (e expansion) check(lim Limits) []string {
	var out []string
	if e.nodes > lim.Each {
		out = append(out, fmt.Sprintf("each and list expand to %d nodes with this state. The limit is %d.", e.nodes, lim.Each))
	}
	if e.images > lim.Images {
		out = append(out, fmt.Sprintf("The document has %d images after each expands. The limit is %d.", e.images, lim.Images))
	}
	if e.rows > lim.Rows {
		out = append(out, fmt.Sprintf("Tables, lists, trees and charts hold %d rows and points with this state. The limit is %d.", e.rows, lim.Rows))
	}
	return out
}
