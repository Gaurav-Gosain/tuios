package loom

import (
	"strconv"

	"tuios.dev/tml"
)

// buildChildren builds the component children of n.
func (s *Surface) buildChildren(n *tml.Node, ptr string, sc *scope, parent *box) []*box {
	var out []*box
	for i, c := range n.Children {
		if c.IsText() {
			continue
		}
		out = append(out, s.build(c, ptr+"/children/"+strconv.Itoa(i), sc, parent)...)
	}
	return out
}

// build turns a node into boxes: none when if is false, one per element
// with each, else one.
func (s *Surface) build(n *tml.Node, ptr string, sc *scope, parent *box) []*box {
	each, ok := n.Attr("each")
	if !ok {
		if b := s.buildOne(n, ptr, sc, parent); b != nil {
			return []*box{b}
		}
		return nil
	}
	_ = each
	elems := s.data(n, "each", s.vars(sc))
	alias := n.AttrString("as")
	if alias == "" {
		alias = "item"
	}
	var out []*box
	for i, el := range elems {
		if !s.exp.Take(1) {
			break
		}
		copyScope := sc.with(alias, el, i)
		if n.Type != "table" {
			if k := s.str(n, "key", s.vars(copyScope)); k != "" {
				copyScope.ident = "k" + k
			}
		}
		if b := s.buildOne(n, ptr, copyScope, parent); b != nil {
			out = append(out, b)
		}
	}
	return out
}

// buildOne builds the box of one node in a scope, or nil when its if is
// false.
func (s *Surface) buildOne(n *tml.Node, ptr string, sc *scope, parent *box) *box {
	// Each box spends its share of the frame's step budget (shareFor), so
	// it evaluates to the same result in a full render and when it is
	// built again alone.
	frame := s.budget
	s.budget = &tml.Budget{Steps: s.boxSteps}
	defer func(b *tml.Budget) {
		s.stats.steps += s.boxSteps - max(b.Steps, 0)
		s.budget = frame
	}(s.budget)
	vars := s.vars(sc)
	if !s.truthy(n, "if", vars) {
		return nil
	}
	b := &box{kind: n.Type, src: n, ptr: ptr, scope: sc, parent: parent, dirty: true}
	s.stats.built++
	b.base = n.ID()
	if b.base == "" {
		b.base = ptr
	}
	b.key = b.base + sc.idents()
	b.hidden = !s.truthy(n, "show", vars)
	b.width = parseSize(s.str(n, "w", vars))
	b.height = parseSize(s.str(n, "h", vars))
	b.minW = parseSize(n.AttrString("min-w"))
	b.maxW = parseSize(n.AttrString("max-w"))
	b.minH = parseSize(n.AttrString("min-h"))
	b.maxH = parseSize(n.AttrString("max-h"))
	b.pad = parsePad(n.AttrString("pad"))
	b.gap = s.count(n, "gap", vars, 0, 0, 100)
	b.grow = s.count(n, "grow", vars, 0, 0, 100)
	b.align = n.AttrString("align")
	b.justify = n.AttrString("justify")

	if n.Type == "form" {
		// form.NAME reads a field's value. Editing comes with the input
		// wave; until then a field holds its first value.
		sc = sc.with("form", s.formValues(n, ptr, vars), 0)
		sc.ident = ""
		b.scope = sc
	}
	switch n.Type {
	case "row", "col", "grid", "stack", "scroll", "split", "form", "tab":
		b.kids = s.buildChildren(n, ptr, sc, b)
		if n.Type == "scroll" {
			b.w = &scrollWidget{}
		}
	case "list":
		b.w = s.buildList(b, vars)
	case "card":
		b.w = s.buildCard(b, vars)
	case "tabs":
		b.w = s.buildTabs(b, vars)
	case "modal":
		// A closed modal takes no space. An open one draws in place; the
		// host floats it in wave 3.
		if !s.flag(n, "open", vars, false) {
			return nil
		}
		b.w = s.buildCard(b, vars)
	default:
		b.w = s.buildLeaf(b, vars)
	}
	s.byPtr[ptr] = append(s.byPtr[ptr], b)
	return b
}

func clampInt(v, lo, hi int) int { return max(lo, min(hi, v)) }

// formValues returns the value of each field of a form, by name: the state
// a field binds, what was typed into it, or its first value.
func (s *Surface) formValues(form *tml.Node, formPtr string, vars map[string]any) map[string]any {
	out := map[string]any{}
	form.Walk(func(n *tml.Node, rel string) bool {
		name := n.AttrString("name")
		if name == "" || n.IsText() {
			return true
		}
		var v any
		if p := n.AttrString("bind"); p != "" {
			if ptr, err := tml.PathPointer(p); err == nil {
				v = s.store.Get(ptr)
			}
		} else {
			key := n.ID()
			if key == "" {
				key = formPtr + rel
			}
			if t, ok := s.view.Text[key]; ok {
				v = t
			}
		}
		if v == nil {
			v, _ = s.value(n, "value", vars)
		}
		if str, ok := v.(string); ok && (n.Type == "toggle" || n.Type == "checkbox") {
			v = str == "true"
		}
		out[name] = v
		return true
	})
	return out
}

// The frame's step budget for bindings, shared by the boxes that evaluate.
const (
	frameSteps  = 1_000_000
	minBoxSteps = 100
)

// shareFor is one box's share of the frame budget among at most n boxes:
// frameSteps/n, and at least minBoxSteps.
func shareFor(n int) int {
	return max(minBoxSteps, frameSteps/max(n, 1))
}

// maxBoxes bounds the boxes a document can build: one per component, and,
// when it repeats anything with each or list, the copies its Each limit
// allows. tml.StoreFor refuses a patch that would pass that limit, so the
// bound holds for every state the surface can reach, and the share stays
// the same across patches: a full and an incremental render agree.
func maxBoxes(doc *tml.Node, lim *tml.Limits) int {
	n, repeats := 0, false
	doc.Walk(func(x *tml.Node, _ string) bool {
		if x.IsText() {
			return true
		}
		n++
		if _, ok := x.Attr("each"); ok || x.Type == "list" {
			repeats = true
		}
		return true
	})
	if repeats {
		each := tml.DefaultLimits().Each
		if lim != nil && lim.Each > 0 {
			each = lim.Each
		}
		n += each
	}
	return n
}
