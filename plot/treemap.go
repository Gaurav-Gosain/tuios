package plot

import (
	"math"
	"slices"
	"sort"
)

// Treemap is a treemap: nested boxes, each as large as its share. A
// section's name and value sit on its top line, a leaf's name and value
// in its middle. Each branch of the root takes a series colour. A label
// too long for its box is shortened.
//
// Boxes places the boxes. TreemapOf lays them out from a tree, or a host
// with its own layout, such as the SVG of a diagram, fills Boxes itself.
type Treemap struct {
	Boxes []TreemapBox // parents before their children
}

// TreemapBox is one box, placed in the whole area as fractions from 0 to 1.
type TreemapBox struct {
	X, Y, W, H float64
	Label      string
	// Value is the value as the box writes it; "" writes none.
	Value string
	// Section says the box holds others.
	Section bool
	// Depth is 0 for the root, 1 for its children, and so on.
	Depth int
}

// TreeItem is a node of the tree TreemapOf lays out. A leaf has a Value;
// a section's value is the sum of its children's.
type TreeItem struct {
	Label    string
	Value    float64
	Children []TreeItem
}

// MaxTreemapBoxes is the most boxes a treemap draws. Past it Draw draws
// nothing, and Fits says so.
const MaxTreemapBoxes = 400

// TreemapOf lays out the items in the whole area: the items of a level side
// by side across, those of the next level down, and so on in turn, each as
// large as its value. format writes a value; nil means Compact.
func TreemapOf(items []TreeItem, format func(float64) string) Treemap {
	if format == nil {
		format = Compact
	}
	var t Treemap
	var total func(it TreeItem, depth int) float64
	total = func(it TreeItem, depth int) float64 {
		if len(it.Children) == 0 || depth > 32 {
			if finite(it.Value) && it.Value > 0 {
				return it.Value
			}
			return 0
		}
		sum := 0.0
		for _, ch := range it.Children {
			sum += total(ch, depth+1)
		}
		return sum
	}
	var place func(list []TreeItem, x, y, w, h float64, depth int, across bool)
	place = func(list []TreeItem, x, y, w, h float64, depth int, across bool) {
		sum := 0.0
		for _, it := range list {
			sum += total(it, depth)
		}
		if sum <= 0 || len(t.Boxes) >= MaxTreemapBoxes {
			return
		}
		// The biggest first, as a treemap is read.
		order := make([]int, len(list))
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(a, b int) bool { return total(list[order[a]], depth) > total(list[order[b]], depth) })
		at := 0.0
		for _, i := range order {
			it := list[i]
			share := total(it, depth) / sum
			if share <= 0 {
				continue
			}
			bx, by, bw, bh := x+at*w, y, share*w, h
			if !across {
				bx, by, bw, bh = x, y+at*h, w, share*h
			}
			at += share
			b := TreemapBox{X: bx, Y: by, W: bw, H: bh, Label: it.Label, Value: format(total(it, depth)), Section: len(it.Children) > 0, Depth: depth}
			t.Boxes = append(t.Boxes, b)
			if b.Section {
				place(it.Children, bx, by, bw, bh, depth+1, !across)
			}
		}
	}
	place(items, 0, 0, 1, 1, 0, true)
	return t
}

type tmRect struct{ x0, y0, x1, y1 int }

// cells places the boxes in a w by h grid, and reports whether every box
// has room for a line round it and a short label.
func (t Treemap) cells(w, h int) ([]tmRect, []int, bool) {
	if len(t.Boxes) == 0 || len(t.Boxes) > MaxTreemapBoxes || w < 3 || h < 3 {
		return nil, nil, false
	}
	sx, sy := float64(w-1), float64(h-1)
	cells := make([]tmRect, len(t.Boxes))
	parent := make([]int, len(t.Boxes))
	deepest := 0
	for i, b := range t.Boxes {
		if !finite(b.X) || !finite(b.Y) || !finite(b.W) || !finite(b.H) {
			return nil, nil, false
		}
		deepest = max(deepest, b.Depth)
		parent[i] = -1
		for j := i - 1; j >= 0 && b.Depth > 0; j-- {
			p := t.Boxes[j]
			if p.Depth == b.Depth-1 && p.Section && b.X >= p.X && b.Y >= p.Y && b.X < p.X+p.W && b.Y < p.Y+p.H {
				parent[i] = j
				break
			}
		}
	}
	deepest = min(deepest, 64)
	// A level at a time, so that a box keeps to the lines its parent ended
	// with. A child shares its parent's sides and bottom, and starts under
	// its top line.
	for depth := 0; depth <= deepest; depth++ {
		for i, b := range t.Boxes {
			if b.Depth != depth {
				continue
			}
			r := tmRect{int(math.Round(b.X * sx)), int(math.Round(b.Y * sy)), int(math.Round((b.X + b.W) * sx)), int(math.Round((b.Y + b.H) * sy))}
			if p := parent[i]; p >= 0 {
				pr := cells[p]
				if r.x0 <= pr.x0+1 {
					r.x0 = pr.x0
				}
				if r.x1 >= pr.x1-1 {
					r.x1 = pr.x1
				}
				if r.y0 <= pr.y0+2 {
					r.y0 = pr.y0 + 1
				}
				if r.y1 >= pr.y1-1 {
					r.y1 = pr.y1
				}
				r = tmRect{max(r.x0, pr.x0), max(r.y0, pr.y0+1), min(r.x1, pr.x1), min(r.y1, pr.y1)}
			}
			cells[i] = r
		}
		// Boxes in one section may share a line, but no more. Two lines side
		// by side become one.
		for i := range cells {
			if t.Boxes[i].Depth != depth {
				continue
			}
			for j := i + 1; j < len(cells); j++ {
				if t.Boxes[j].Depth != depth || parent[i] != parent[j] {
					continue
				}
				a, b := &cells[i], &cells[j]
				across := a.y0 < b.y1 && b.y0 < a.y1
				along := a.x0 < b.x1 && b.x0 < a.x1
				switch {
				case across && a.x1+1 == b.x0:
					b.x0 = a.x1
				case across && b.x1+1 == a.x0:
					a.x0 = b.x1
				case along && a.y1+1 == b.y0:
					b.y0 = a.y1
				case along && b.y1+1 == a.y0:
					a.y0 = b.y1
				}
				if a.x1 <= b.x0 || b.x1 <= a.x0 || a.y1 <= b.y0 || b.y1 <= a.y0 {
					continue
				}
				return nil, nil, false
			}
		}
	}
	for i, b := range t.Boxes {
		r := cells[i]
		if r.x1-r.x0 < 2 || r.y1-r.y0 < 2 {
			return nil, nil, false
		}
		// A label shortened past three cells says nothing.
		if b.Section && min(width(b.Label), 4)+4 > r.x1-r.x0-1 {
			return nil, nil, false
		}
		lines := 1
		if b.Value != "" && !b.Section {
			lines = 2
		}
		if !b.Section && (min(width(b.Label), 4) > r.x1-r.x0-1 || lines > r.y1-r.y0-1) {
			return nil, nil, false
		}
	}
	return cells, parent, true
}

// Fits reports whether the boxes can be drawn in a w by h grid: each with
// room for a line round it and a label of a few cells.
func (t Treemap) Fits(w, h int) bool {
	_, _, ok := t.cells(w, h)
	return ok
}

// The light box-drawing glyph for each set of strokes: up 1, right 2,
// down 4, left 8.
var tmJoins = [16]string{
	0: " ", 1: "│", 4: "│", 5: "│", 2: "─", 8: "─", 10: "─",
	6: "┌", 12: "┐", 3: "└", 9: "┘", 7: "├", 13: "┤", 14: "┬", 11: "┴", 15: "┼",
}

// Draw draws the treemap into a w by h grid, or leaves it blank when the
// boxes do not fit: see Fits.
func (t Treemap) Draw(w, h int, m Mode) *Grid {
	g := NewGrid(w, h)
	cells, parent, ok := t.cells(g.W, g.H)
	if !ok {
		return g
	}
	// The colour of each box: that of its branch of the root.
	role := make([]Role, len(t.Boxes))
	next := 0
	for i := range t.Boxes {
		top := i
		for parent[top] >= 0 && t.Boxes[parent[top]].Depth > 0 {
			top = parent[top]
		}
		switch {
		case t.Boxes[i].Depth == 0:
			role[i] = Mute
		case top == i:
			role[i] = SeriesRole(next)
			next++
		default:
			role[i] = role[top]
		}
	}
	// The lines, as strokes a cell, joined. A cell takes the colour of the
	// first box that draws on it: the root's frame first, then the rest from
	// the last, and then each box's top line, which carries its name.
	mask := make([]uint8, g.W*g.H)
	lineRole := make([]Role, g.W*g.H)
	stroke := func(x, y int, bits uint8, r Role) {
		if x < 0 || y < 0 || x >= g.W || y >= g.H {
			return
		}
		k := y*g.W + x
		if mask[k] == 0 {
			lineRole[k] = r
		}
		mask[k] |= bits
	}
	frame := func(i int) {
		r, c := cells[i], role[i]
		for x := r.x0; x <= r.x1; x++ {
			var bits uint8
			if x > r.x0 {
				bits |= 8
			}
			if x < r.x1 {
				bits |= 2
			}
			stroke(x, r.y0, bits, c)
			stroke(x, r.y1, bits, c)
		}
		for y := r.y0; y <= r.y1; y++ {
			var bits uint8
			if y > r.y0 {
				bits |= 1
			}
			if y < r.y1 {
				bits |= 4
			}
			stroke(r.x0, y, bits, c)
			stroke(r.x1, y, bits, c)
		}
	}
	for i, b := range t.Boxes {
		if b.Depth == 0 {
			frame(i)
		}
	}
	for i, v := range slices.Backward(t.Boxes) {
		if v.Depth > 0 {
			frame(i)
		}
	}
	for i, b := range t.Boxes {
		if b.Depth == 0 {
			continue
		}
		r := cells[i]
		for x := r.x0 + 1; x < r.x1; x++ {
			if k := r.y0*g.W + x; mask[k] != 0 {
				lineRole[k] = role[i]
			}
		}
	}
	for y := 0; y < g.H; y++ {
		for x := 0; x < g.W; x++ {
			k := y*g.W + x
			if mask[k] == 0 {
				continue
			}
			s := tmJoins[mask[k]]
			if m == ASCII {
				s = map[bool]string{true: "-", false: "|"}[mask[k]&10 != 0]
				if mask[k]&10 != 0 && mask[k]&5 != 0 {
					s = "+"
				}
			}
			g.set(x, y, s, lineRole[k])
		}
	}
	tail := ellipsis(m)
	for i, b := range t.Boxes {
		r := cells[i]
		inner := r.x1 - r.x0 - 1
		if b.Section {
			label := truncate(b.Label, inner-3, tail)
			x := g.Text(r.x0+2, r.y0, " "+label+" ", Fg, false)
			if b.Value != "" && width(label)+width(b.Value)+6 <= inner {
				g.Text(x, r.y0, " "+b.Value+" ", Dim, false)
			}
			continue
		}
		lines := []string{truncate(b.Label, inner, tail)}
		if b.Value != "" && width(b.Value) <= inner && r.y1-r.y0-1 >= 2 {
			lines = append(lines, b.Value)
		}
		top := r.y0 + 1 + (r.y1-r.y0-1-len(lines))/2
		for k, line := range lines {
			lr := Fg
			if k > 0 {
				lr = Dim
			}
			g.Text(r.x0+1+(inner-width(line))/2, top+k, line, lr, false)
		}
	}
	return g
}
