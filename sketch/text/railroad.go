package text

import (
	"math"
	"strconv"
	"strings"

	"tuios.dev/sketch"
	"tuios.dev/sketch/internal/types"
)

// A railroad diagram is drawn as the SVG draws it, in cells: each rule's name, then its track
// from a start mark to an end mark. A terminal is a rounded box, a nonterminal a square one and
// a special a dashed one. A choice stacks its branches between two rails, an optional part has a
// track over it that passes it by, and a repetition a track under it that runs back, with its
// count. A sequence too wide for the columns folds into rows, as the SVG does for a narrow
// width. A rule that still does not fit is written out in EBNF.

const (
	rrTerminal = iota
	rrNonTerminal
	rrSpecial
	rrSequence
	rrFolded
	rrChoice
	rrBypass
	rrLoop
)

// rrBox is a laid-out element in cells. The track enters at the left edge and leaves at the
// right edge on its baseline, with up rows above it and down rows below it.
type rrBox struct {
	kind         int
	w, up, down  int
	kids         []*rrBox
	text         string
	reach        int
	rowOffsets   []int
	branchOffset []int
}

// rrCount is the count a repetition shows, as pele writes it.
func rrCount(min, max float64) string {
	if math.IsInf(max, 1) && min <= 1 {
		return ""
	}
	str := func(v float64) string {
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return "∞"
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	switch {
	case min == max:
		return str(min) + "×"
	case math.IsInf(max, 1):
		return "≥" + str(min) + "×"
	case min == 0:
		return "≤" + str(max) + "×"
	}
	return str(min) + "–" + str(max) + "×"
}

// foldTree builds a value for every node of a tree from the values of its children, without
// recursion, so that a deep tree does not exhaust the stack.
func foldTree[R any](root *types.GeoRailNode, build func(*types.GeoRailNode, []R) R) R {
	nodes := []*types.GeoRailNode{root}
	done := [][]R{nil}
	for {
		top := len(nodes) - 1
		if len(done[top]) < len(nodes[top].Kids) {
			nodes = append(nodes, nodes[top].Kids[len(done[top])])
			done = append(done, nil)
			continue
		}
		value := build(nodes[top], done[top])
		if top == 0 {
			return value
		}
		nodes, done = nodes[:top], done[:top]
		done[top-1] = append(done[top-1], value)
	}
}

func rrBypassOf(kid *rrBox) *rrBox {
	return &rrBox{kind: rrBypass, w: kid.w + 4, up: kid.up + 1, down: kid.down, kids: []*rrBox{kid}, reach: kid.up + 1}
}

// rrMeasure lays a rule out to fit room columns where a sequence can fold.
func rrMeasure(root *types.GeoRailNode, room int) *rrBox {
	budgets := map[*types.GeoRailNode]int{}
	type item struct {
		node *types.GeoRailNode
		room int
	}
	pending := []item{{root, room}}
	for i := 0; i < len(pending); i++ {
		node, width := pending[i].node, pending[i].room
		budgets[node] = width
		around := 0
		switch node.Kind {
		case "choice", "optional":
			around = 4
		case "repetition":
			around = 4
			if node.Min == 0 {
				around = 8
			}
		case "sequence":
			around = 6
		}
		for _, kid := range node.Kids {
			pending = append(pending, item{kid, max(10, width-around)})
		}
	}
	return foldTree(root, func(node *types.GeoRailNode, kids []*rrBox) *rrBox {
		switch node.Kind {
		case "terminal", "nonterminal", "special":
			kind := rrTerminal
			if node.Kind == "nonterminal" {
				kind = rrNonTerminal
			} else if node.Kind == "special" {
				kind = rrSpecial
			}
			return &rrBox{kind: kind, w: width(node.Text) + 4, up: 1, down: 1, text: node.Text}
		case "sequence":
			if len(kids) == 0 {
				return &rrBox{kind: rrSequence, w: 2}
			}
			seq := &rrBox{kind: rrSequence, kids: kids, w: 2 * (len(kids) - 1)}
			for _, kid := range kids {
				seq.w += kid.w
				seq.up = max(seq.up, kid.up)
				seq.down = max(seq.down, kid.down)
			}
			room := budgets[node]
			if seq.w <= room || len(kids) < 2 {
				return seq
			}
			var rows []*rrBox
			var row []*rrBox
			rowW := 0
			flush := func() {
				r := &rrBox{kind: rrSequence, kids: row, w: rowW}
				for _, kid := range row {
					r.up = max(r.up, kid.up)
					r.down = max(r.down, kid.down)
				}
				rows = append(rows, r)
				row, rowW = nil, 0
			}
			for _, kid := range kids {
				if len(row) > 0 && rowW+2+kid.w > max(10, room-6) {
					flush()
				}
				if len(row) > 0 {
					rowW += 2
				}
				rowW += kid.w
				row = append(row, kid)
			}
			flush()
			if len(rows) < 2 {
				return seq
			}
			folded := &rrBox{kind: rrFolded, kids: rows, up: rows[0].up, rowOffsets: make([]int, len(rows))}
			widest, offset := 0, 0
			for i, r := range rows {
				widest = max(widest, r.w)
				if i > 0 {
					offset += rows[i-1].down + 2 + r.up
				}
				folded.rowOffsets[i] = offset
			}
			folded.w = widest + 6
			folded.down = offset + rows[len(rows)-1].down
			return folded
		case "choice":
			c := &rrBox{kind: rrChoice, kids: kids, branchOffset: make([]int, len(kids))}
			inner, offset := 0, 0
			for i, kid := range kids {
				inner = max(inner, kid.w)
				if i > 0 {
					offset += kids[i-1].down + 1 + kid.up
				}
				c.branchOffset[i] = offset
			}
			c.w = inner + 4
			if len(kids) > 0 {
				c.up = kids[0].up
				c.down = offset + kids[len(kids)-1].down
			}
			return c
		case "optional":
			if len(kids) == 0 {
				return &rrBox{kind: rrSequence, w: 2}
			}
			return rrBypassOf(kids[0])
		}
		// A repetition.
		if len(kids) == 0 {
			return &rrBox{kind: rrSequence, w: 2}
		}
		kid := kids[0]
		if node.Min == 1 && node.Max == 1 {
			return kid
		}
		count := rrCount(node.Min, node.Max)
		loop := &rrBox{kind: rrLoop, w: max(kid.w, width(count)) + 4, up: kid.up, down: kid.down + 1, kids: []*rrBox{kid}, text: count, reach: kid.down + 1}
		if count != "" {
			loop.down++
		}
		if node.Min == 0 {
			return rrBypassOf(loop)
		}
		return loop
	})
}

// rrPlace draws a box with its left edge at x and its baseline at y.
func rrPlace(c *canvas, root *rrBox, x0, y0 int) {
	type item struct {
		box  *rrBox
		x, y int
	}
	stack := []item{{root, x0, y0}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		b, x, y := it.box, it.x, it.y
		right := x + b.w - 1
		switch b.kind {
		case rrTerminal, rrNonTerminal, rrSpecial:
			weight, role, textRole := uint8(light), uint8(roleBox), uint8(roleText)
			if b.kind == rrNonTerminal {
				role, textRole = roleFrame, roleTitle
			} else if b.kind == rrSpecial {
				weight, role, textRole = dotted, roleFrame, roleNote
			}
			c.hline(x, right, y-1, weight, role)
			c.hline(x, right, y+1, weight, role)
			c.vline(x, y-1, y+1, weight, role)
			c.vline(right, y-1, y+1, weight, role)
			if b.kind == rrTerminal {
				c.put(x, y-1, '╭', role)
				c.put(right, y-1, '╮', role)
				c.put(x, y+1, '╰', role)
				c.put(right, y+1, '╯', role)
			}
			c.text(x+2, y, b.text, textRole)
		case rrSequence:
			if len(b.kids) == 0 {
				c.hline(x, right, y, light, roleLine)
				continue
			}
			kx := x
			for i, kid := range b.kids {
				stack = append(stack, item{kid, kx, y})
				if i+1 < len(b.kids) {
					c.hline(kx+kid.w-1, kx+kid.w+2, y, light, roleLine)
				}
				kx += kid.w + 2
			}
		case rrFolded:
			start := x + 2
			inner, outer := right-2, right-1
			c.hline(x, start, y, light, roleLine)
			for i, row := range b.kids {
				ry := y + b.rowOffsets[i]
				stack = append(stack, item{row, start, ry})
				end := start + row.w - 1
				if i+1 < len(b.kids) {
					gap := ry + row.down + 1
					next := y + b.rowOffsets[i+1]
					c.hline(end, inner, ry, light, roleLine)
					c.vline(inner, ry, gap, light, roleLine)
					c.hline(x+1, inner, gap, light, roleLine)
					c.vline(x+1, gap, next, light, roleLine)
					c.hline(x+1, start, next, light, roleLine)
				} else {
					c.hline(end, outer, ry, light, roleLine)
					c.vline(outer, y, ry, light, roleLine)
					c.hline(outer, right, y, light, roleLine)
				}
			}
		case rrChoice:
			kx := x + 2
			prev := y
			for i, kid := range b.kids {
				ky := y + b.branchOffset[i]
				if i == 0 {
					c.hline(x, kx, y, light, roleLine)
					c.hline(kx+kid.w-1, right, y, light, roleLine)
				} else {
					// From the branch above, not from the top: the strokes are the same, and
					// many branches cost linear time.
					c.vline(x+1, prev, ky, light, roleLine)
					c.hline(x+1, kx, ky, light, roleLine)
					c.hline(kx+kid.w-1, right-1, ky, light, roleLine)
					c.vline(right-1, prev, ky, light, roleLine)
				}
				prev = ky
				stack = append(stack, item{kid, kx, ky})
			}
		case rrBypass:
			kid := b.kids[0]
			kx := x + 2
			top := y - b.reach
			c.hline(x, kx, y, light, roleLine)
			c.hline(kx+kid.w-1, right, y, light, roleLine)
			c.vline(x+1, top, y, light, roleLine)
			c.hline(x+1, right-1, top, light, roleLine)
			c.vline(right-1, top, y, light, roleLine)
			stack = append(stack, item{kid, kx, y})
		case rrLoop:
			kid := b.kids[0]
			kx := x + (b.w-kid.w)/2
			ly := y + b.reach
			c.hline(x, kx, y, light, roleLine)
			c.hline(kx+kid.w-1, right, y, light, roleLine)
			c.vline(x+1, y, ly, light, roleLine)
			c.hline(x+1, right-1, ly, light, roleLine)
			c.vline(right-1, y, ly, light, roleLine)
			c.put(x+b.w/2, ly, '◀', roleLine)
			if b.text != "" {
				c.text(x+(b.w-width(b.text))/2, ly+1, b.text, roleNote)
			}
			stack = append(stack, item{kid, kx, y})
		}
	}
}

// rrText writes a rule's tree in EBNF, for a rule too wide to draw. A choice inside another
// node is in parentheses.
func rrText(root *types.GeoRailNode) (string, bool) {
	type out struct {
		s      string
		choice bool
	}
	total := 0
	r := foldTree(root, func(node *types.GeoRailNode, kids []out) out {
		part := func(k out) string {
			if k.choice {
				return "( " + k.s + " )"
			}
			return k.s
		}
		var s string
		choice := false
		switch node.Kind {
		case "terminal":
			s = strconv.Quote(node.Text)
			if !strings.Contains(node.Text, `"`) && !strings.Contains(node.Text, `\`) {
				s = `"` + node.Text + `"`
			}
		case "nonterminal":
			s = node.Text
		case "special":
			s = "? " + node.Text + " ?"
		case "sequence":
			parts := make([]string, len(kids))
			for i, k := range kids {
				parts[i] = part(k)
			}
			s = strings.Join(parts, " ")
		case "choice":
			parts := make([]string, len(kids))
			for i, k := range kids {
				parts[i] = k.s
			}
			s, choice = strings.Join(parts, " | "), true
		case "optional":
			if len(kids) > 0 {
				s = "[ " + kids[0].s + " ]"
			}
		default:
			if len(kids) > 0 {
				k := kids[0]
				switch {
				case node.Min == 0 && math.IsInf(node.Max, 1):
					s = "{ " + k.s + " }"
				case node.Min == 1 && math.IsInf(node.Max, 1):
					s = part(k) + "+"
				default:
					s = part(k) + " " + rrCount(node.Min, node.Max)
				}
			}
		}
		total += len(s)
		return out{s, choice}
	})
	return r.s, total <= MaxCells
}

func drawRailroad(l *sketch.Layout, opt Options) (*canvas, error) {
	rr := l.Railroad
	if rr == nil {
		return nil, ErrUnsupported
	}
	room := 1 << 20
	if opt.Cols > 0 {
		room = opt.Cols - 5
	}
	title := rr.Title
	if opt.Cols > 0 && widest(title) > opt.Cols {
		title = wrapCells(strings.Join(title, " "), opt.Cols, 1000)
	}
	type drawn struct {
		name  string
		box   *rrBox
		lines []string
	}
	rules := make([]drawn, len(rr.Rules))
	w, h := widest(title), len(title)
	for i, rule := range rr.Rules {
		d := drawn{name: rule.Name}
		if rule.Root != nil {
			if box := rrMeasure(rule.Root, max(10, room)); box.w+5 <= room+5 && box.w < 1<<20 && box.up+box.down < 1<<20 {
				d.box = box
			}
		}
		if d.box == nil {
			s, ok := "", true
			if rule.Root != nil {
				s, ok = rrText(rule.Root)
			}
			if !ok {
				return nil, ErrTooLarge
			}
			cols := 1 << 20
			if opt.Cols > 0 {
				cols = opt.Cols - 2
			}
			d.lines = wrapCells(s, max(1, cols), 1<<20)
		}
		names := []string{d.name}
		if opt.Cols > 0 && width(d.name) > opt.Cols {
			names = wrapCells(d.name, opt.Cols, 1000)
		}
		rules[i] = d
		if i > 0 || len(title) > 0 {
			h++
		}
		h += len(names)
		w = max(w, widest(names))
		if d.box != nil {
			w = max(w, d.box.w+5)
			h += d.box.up + d.box.down + 1
		} else {
			w = max(w, widest(d.lines)+2)
			h += len(d.lines)
		}
		if int64(w)*int64(h) > MaxCells {
			return nil, ErrTooLarge
		}
	}
	if opt.Cols > 0 && w > opt.Cols {
		return nil, ErrTooWide
	}
	c, err := newCanvas(max(w, 1), max(h, 1))
	if err != nil {
		return nil, err
	}
	y := 0
	for _, line := range title {
		c.text((w-width(line))/2, y, line, roleTitle)
		y++
	}
	for i, d := range rules {
		if i > 0 || len(title) > 0 {
			y++
		}
		names := []string{d.name}
		if opt.Cols > 0 && width(d.name) > opt.Cols {
			names = wrapCells(d.name, opt.Cols, 1000)
		}
		for _, name := range names {
			c.text(0, y, name, roleTitle)
			y++
		}
		if d.box == nil {
			for _, line := range d.lines {
				c.text(2, y, line, roleText)
				y++
			}
			continue
		}
		base := y + d.box.up
		c.put(0, base, '●', roleLine)
		c.hline(1, 2, base, light, roleLine)
		rrPlace(c, d.box, 2, base)
		end := 2 + d.box.w - 1
		c.hline(end, end+2, base, light, roleLine)
		c.put(end+2, base, '●', roleLine)
		y = base + d.box.down + 1
	}
	return c, nil
}
