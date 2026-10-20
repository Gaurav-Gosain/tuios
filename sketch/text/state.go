package text

import (
	"tuios.dev/sketch"
	"tuios.dev/sketch/internal/types"
)

// State diagrams: states as rounded boxes, a description under a rule,
// composite states as frames around what they hold, ● for the start, ◉ for
// the end, a small diamond for a choice and a bar for a fork or a join.

var stateKind = graphKind{
	size: func(n *types.GeoNode) (int, int, bool) {
		switch n.Shape {
		case "state", "note":
			w, h := widest(n.Label)+4, len(n.Label)+2
			for _, s := range n.Sections {
				w = max(w, widest(s)+4)
				h += 1 + len(s)
			}
			return w, h, true
		case "choice":
			return 4, 2, true
		case "hidden":
			return 1, 1, true
		case "fork":
			if n.H > n.W {
				return 1, 3, true
			}
		}
		return 0, 0, false
	},
	node: func(c *canvas, b rect, n *types.GeoNode) bool {
		switch n.Shape {
		case "state", "note":
			role := uint8(roleBox)
			if n.Shape == "note" {
				role = roleNote
			}
			frame(c, b, role, n.Shape == "state")
			y := b.y0 + 1
			for _, l := range n.Label {
				centred(c, b.x0, b.x1, y, l, roleText)
				y++
			}
			for _, s := range n.Sections {
				rule(c, b, y, role)
				y++
				for _, l := range s {
					c.text(b.x0+2, y, l, roleText)
					y++
				}
			}
		case "choice":
			for y := b.y0; y <= b.y1; y++ {
				for x := b.x0; x <= b.x1; x++ {
					c.clear(x, y)
				}
			}
			x := (b.x0 + b.x1) / 2
			c.text(x, b.y0, "╱╲", roleBox)
			c.text(x, b.y0+1, "╲╱", roleBox)
		case "hidden":
			c.clear(b.x0, b.y0)
		case "fork":
			if n.H <= n.W {
				// A bar across a flow that runs down: the flowchart's.
				return false
			}
			for y := b.y0; y <= b.y1; y++ {
				c.put((b.x0+b.x1)/2, y, '┃', roleBox)
			}
		default:
			return false
		}
		return true
	},
	frame: func(n *types.GeoNode) (bool, uint8, bool) {
		switch n.Shape {
		case "hidden":
			return false, light, false
		case "region":
			return true, dotted, false
		}
		return true, light, true
	},
}

func drawState(l *sketch.Layout, opt Options) (*canvas, error) {
	// The start and the end are one cell, as a flowchart's small circles are.
	for i := range l.Nodes {
		switch l.Nodes[i].Shape {
		case "state-start":
			l.Nodes[i].Shape = "f-circ"
		case "state-end":
			l.Nodes[i].Shape = "fr-circ"
		}
	}
	c, err := drawGraph(l, opt, &stateKind)
	if err != nil {
		return nil, err
	}
	return withTitle(c, l.Title, opt.Cols)
}
