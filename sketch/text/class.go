package text

import (
	"tuios.dev/sketch"
	"tuios.dev/sketch/internal/types"
)

// Class diagrams: a class is a box with its name, its attributes and its
// methods, each under a rule. Relations end in UML marks: △ for
// inheritance, ◆ for composition, ◇ for aggregation, an arrowhead for a
// dependency and ○ for a lollipop. Cardinalities sit beside the ends.

// classSections are a class's sections without the empty ones at the end:
// in text, a rule over nothing only adds a line.
func classSections(n *types.GeoNode) [][]string {
	s := n.Sections
	for len(s) > 0 && len(s[len(s)-1]) == 0 {
		s = s[:len(s)-1]
	}
	return s
}

var classKind = graphKind{
	size: func(n *types.GeoNode) (int, int, bool) {
		switch n.Shape {
		case "class", "note":
			w, h := widest(n.Label)+4, len(n.Label)+2
			for _, s := range classSections(n) {
				w = max(w, widest(s)+4)
				h += 1 + len(s)
			}
			return w, h, true
		}
		return 0, 0, false
	},
	node: func(c *canvas, b rect, n *types.GeoNode) bool {
		role := uint8(roleBox)
		switch n.Shape {
		case "class":
		case "note":
			role = roleNote
		default:
			return false
		}
		frame(c, b, role, false)
		y := b.y0 + 1
		for k, l := range n.Label {
			r := uint8(roleText)
			if k == len(n.Label)-1 && n.Shape == "class" {
				r = roleTitle
			} else if n.Shape == "class" {
				// The annotations above the name.
				r = roleNote
			}
			centred(c, b.x0, b.x1, y, l, r)
			y++
		}
		for _, s := range classSections(n) {
			rule(c, b, y, role)
			y++
			for _, l := range s {
				c.text(b.x0+2, y, l, roleText)
				y++
			}
		}
		return true
	},
	head: func(c *canvas, at point, side int, kind string) bool {
		switch kind {
		case "extension":
			c.put(at.x, at.y, [4]rune{'▽', '◁', '△', '▷'}[side], roleHead)
		case "composition":
			c.put(at.x, at.y, '◆', roleHead)
		case "aggregation":
			c.put(at.x, at.y, '◇', roleHead)
		case "dependency":
			c.put(at.x, at.y, [4]rune{'▼', '◄', '▲', '►'}[side], roleHead)
		case "lollipop":
			c.put(at.x, at.y, '○', roleHead)
		default:
			return false
		}
		return true
	},
}

func drawClass(l *sketch.Layout, opt Options) (*canvas, error) {
	c, err := drawGraph(l, opt, &classKind)
	if err != nil {
		return nil, err
	}
	return withTitle(c, l.Title, opt.Cols)
}
