package text

import (
	"tuios.dev/sketch"
	"tuios.dev/sketch/internal/types"
)

// ER diagrams: an entity is a box with its name over a table of its
// attributes: type, name, keys and comment. Each end of a relationship says
// its cardinality: 1, 0..1, 1..* or 0..*. A non-identifying relationship is a
// dotted line.

// erCardinality is the text for each crow's foot marker.
var erCardinality = map[string]string{
	"only_one":     "1",
	"zero_or_one":  "0..1",
	"one_or_more":  "1..*",
	"zero_or_more": "0..*",
}

// erColumns is the width of each column of an entity's table, 0 for a column
// no row fills.
func erColumns(table [][4]string) [4]int {
	var w [4]int
	for _, row := range table {
		for k, cell := range row {
			w[k] = max(w[k], width(cell))
		}
	}
	return w
}

var erKind = graphKind{
	size: func(n *types.GeoNode) (int, int, bool) {
		if n.Shape != "entity" {
			return 0, 0, false
		}
		w, h := widest(n.Label)+4, len(n.Label)+2
		if len(n.Table) > 0 {
			inner := -2
			for _, cw := range erColumns(n.Table) {
				if cw > 0 {
					inner += cw + 2
				}
			}
			w = max(w, inner+4)
			h += 1 + len(n.Table)
		}
		return w, h, true
	},
	node: func(c *canvas, b rect, n *types.GeoNode) bool {
		if n.Shape != "entity" {
			return false
		}
		frame(c, b, roleBox, false)
		y := b.y0 + 1
		for _, l := range n.Label {
			centred(c, b.x0, b.x1, y, l, roleTitle)
			y++
		}
		if len(n.Table) == 0 {
			return true
		}
		rule(c, b, y, roleBox)
		y++
		cols := erColumns(n.Table)
		// Type and keys are quieter than the name, as in the SVG.
		roles := [4]uint8{roleLine, roleText, roleNote, roleLine}
		for _, row := range n.Table {
			x := b.x0 + 2
			for k, cell := range row {
				if cols[k] == 0 {
					continue
				}
				c.text(x, y, cell, roles[k])
				x += cols[k] + 2
			}
			y++
		}
		return true
	},
	head: func(c *canvas, at point, side int, kind string) bool {
		if kind == "md_parent" {
			c.put(at.x, at.y, '◇', roleHead)
		}
		return true
	},
	endText: func(e *types.GeoEdge, last bool) string {
		if last {
			return erCardinality[e.End]
		}
		return erCardinality[e.Start]
	},
}

func drawER(l *sketch.Layout, opt Options) (*canvas, error) {
	c, err := drawGraph(l, opt, &erKind)
	if err != nil {
		return nil, err
	}
	return withTitle(c, l.Title, opt.Cols)
}
