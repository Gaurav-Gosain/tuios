package text

import (
	"math"

	"tuios.dev/sketch"
	"tuios.dev/sketch/internal/types"
)

// A treemap is drawn as nested boxes, placed and sized as in the SVG and scaled to cells:
// a section shows its name and value on its top line, a leaf its name with its value under
// it. Each branch of the root takes a series colour. When the columns are too few for every
// label, the boxes keep the width there is and a label too long for its box is shortened.
// When boxes are too small to draw at all, it is drawn as an indented tree with the values
// instead.

// treemapBoxes is at most the count of boxes drawn as boxes; past it the tree is clearer.
const treemapBoxes = 400

func drawTreemap(l *sketch.Layout, opt Options) (*canvas, error) {
	t := l.Treemap
	if t == nil {
		return nil, ErrUnsupported
	}
	if c := treemapAsBoxes(t, opt); c != nil {
		return c, nil
	}
	return treemapAsTree(t, opt)
}

// leafText is the lines a leaf shows.
func leafText(b types.GeoTreeBox) []string {
	if b.Value == "" {
		return []string{b.Label}
	}
	return []string{b.Label, b.Value}
}

// sectionText is what a section shows on its top line.
func sectionText(b types.GeoTreeBox) string {
	if b.Value == "" {
		return " " + b.Label + " "
	}
	return " " + b.Label + "  " + b.Value + " "
}

func treemapAsBoxes(t *types.GeoTreemap, opt Options) *canvas {
	if len(t.Boxes) == 0 || len(t.Boxes) > treemapBoxes || t.Width <= 0 || t.Height <= 0 {
		return nil
	}
	// The scale at which every label fits its box; a cell is about twice as tall as wide.
	sx, sy := 1.0/12, 0.0
	for _, b := range t.Boxes {
		if b.W <= 0 || b.H <= 0 {
			continue
		}
		if b.Section {
			sx = math.Max(sx, float64(width(sectionText(b))+4)/b.W)
			sy = math.Max(sy, 3/b.H)
		} else {
			lines := leafText(b)
			sx = math.Max(sx, float64(widest(lines)+2)/b.W)
			sy = math.Max(sy, float64(len(lines)+2)/b.H)
		}
	}
	sy = math.Max(sy, sx*0.45)
	if opt.Cols > 0 && int(math.Round(t.Width*sx))+1 > opt.Cols {
		// Narrower than every label needs: labels are shortened.
		sx = float64(opt.Cols-1) / t.Width
	}
	w := int(math.Round(t.Width*sx)) + 1
	if opt.Cols > 0 && max(w, widest(t.Title)) > opt.Cols {
		return nil
	}
	h := int(math.Round(t.Height*sy)) + 1
	if int64(w)*int64(h) > MaxCells {
		return nil
	}
	// Each box keeps to its parent: the nearest section before it, one level up, that holds it.
	// A child shares its parent's sides and bottom, and starts under its top line.
	cells := make([]rect, len(t.Boxes))
	parent := make([]int, len(t.Boxes))
	deepest := 0
	for i, b := range t.Boxes {
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
	// A level at a time, so that a box keeps to the lines its parent ended with.
	for depth := 0; depth <= deepest; depth++ {
		for i, b := range t.Boxes {
			if b.Depth != depth {
				continue
			}
			r := rect{int(math.Round(b.X * sx)), int(math.Round(b.Y * sy)), int(math.Round((b.X + b.W) * sx)), int(math.Round((b.Y + b.H) * sy))}
			if p := parent[i]; p >= 0 {
				// An edge a cell from its parent's takes the parent's line. A child starts under its parent's top line.
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
				r = rect{max(r.x0, pr.x0), max(r.y0, pr.y0+1), min(r.x1, pr.x1), min(r.y1, pr.y1)}
			}
			cells[i] = r
		}
		// Boxes in one section may share a line, but no more. Two lines side by side become one.
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
				return nil
			}
		}
	}
	for i, b := range t.Boxes {
		r := cells[i]
		if r.x1-r.x0 < 2 || r.y1-r.y0 < 2 {
			return nil
		}
		// A label shortened past three cells says nothing.
		if b.Section && min(width(b.Label), 4)+4 > r.x1-r.x0-1 {
			return nil
		}
		if !b.Section && (min(width(b.Label), 4) > r.x1-r.x0-1 || len(leafText(b)) > r.y1-r.y0-1) {
			return nil
		}
	}
	// branch is the series colour of each box: that of its branch of the root.
	branch := make([]uint8, len(t.Boxes))
	next := 0
	for i := range t.Boxes {
		top := i
		for parent[top] >= 0 && t.Boxes[parent[top]].Depth > 0 {
			top = parent[top]
		}
		switch {
		case t.Boxes[i].Depth == 0:
			branch[i] = roleFrame
		case top == i:
			branch[i] = seriesRole(next)
			next++
		default:
			branch[i] = branch[top]
		}
	}
	titleH := 0
	if len(t.Title) > 0 {
		titleH = len(t.Title) + 1
	}
	w = max(w, widest(t.Title))
	c, err := newCanvas(w, h+titleH)
	if err != nil {
		return nil
	}
	for i, line := range t.Title {
		c.text((w-width(line))/2, i, line, roleTitle)
	}
	// The frames first: the root's, then the rest from the last. A line two
	// boxes share takes the colour of the first box drawn on it.
	frame := func(i int) {
		r := cells[i]
		c.box(r.x0, r.y0+titleH, r.x1-r.x0+1, r.y1-r.y0+1, nil, branch[i], roleText)
	}
	for i, b := range t.Boxes {
		if b.Depth == 0 {
			frame(i)
		}
	}
	for i := len(t.Boxes) - 1; i >= 0; i-- {
		if t.Boxes[i].Depth > 0 {
			frame(i)
		}
	}
	// A box's top line is its own, whatever it shares: it carries its name.
	// The corners stay as they were: they belong to the sides as well.
	for i, b := range t.Boxes {
		if b.Depth == 0 {
			continue
		}
		r := cells[i]
		for x := r.x0 + 1; x < r.x1; x++ {
			if p := c.at(x, r.y0+titleH); p != nil && p.mask != 0 {
				p.role = branch[i]
			}
		}
	}
	for i, b := range t.Boxes {
		r := cells[i]
		y0 := r.y0 + titleH
		inner := r.x1 - r.x0 - 1
		if b.Section {
			label := fit(b.Label, inner-3)
			c.text(r.x0+2, y0, " "+label+" ", roleHead)
			if b.Value != "" && width(sectionText(b))+2 <= inner {
				c.text(r.x0+2+width(label)+2, y0, " "+b.Value+" ", roleNote)
			}
			continue
		}
		lines := leafText(b)
		lines[0] = fit(lines[0], inner)
		if len(lines) > 1 && width(lines[1]) > inner {
			lines = lines[:1]
		}
		top := y0 + 1 + (r.y1-r.y0-1-len(lines))/2
		for k, line := range lines {
			role := uint8(roleText)
			if k > 0 {
				role = roleNote
			}
			c.text(r.x0+1+(r.x1-r.x0-1-width(line))/2, top+k, line, role)
		}
	}
	return c
}

func treemapAsTree(t *types.GeoTreemap, opt Options) (*canvas, error) {
	n := len(t.Tree)
	// last[i] says whether node i is the last of its siblings, found from the end.
	last := make([]bool, n)
	// seen[d] says whether a later node of depth d shares the parent of the current one.
	var seen []bool
	for i := n - 1; i >= 0; i-- {
		d := min(t.Tree[i].Depth, 64)
		last[i] = !(d < len(seen) && seen[d])
		// A node ends the runs of the nodes deeper than it.
		for len(seen) <= d {
			seen = append(seen, false)
		}
		seen = seen[:d+1]
		seen[d] = true
	}
	lines := make([][3]string, n)
	w := widest(t.Title)
	// open[d] says whether the line of depth d continues below the current node.
	var open []bool
	for i, b := range t.Tree {
		d := min(b.Depth, 64)
		open = append(open[:min(len(open), d)], make([]bool, max(0, d-len(open)))...)
		prefix := ""
		for k := 1; k < d; k++ {
			if k < len(open) && open[k] {
				prefix += "│ "
			} else {
				prefix += "  "
			}
		}
		if d > 0 {
			if last[i] {
				prefix += "└ "
			} else {
				prefix += "├ "
			}
		}
		for len(open) <= d {
			open = append(open, false)
		}
		open[d] = !last[i]
		lines[i] = [3]string{prefix, b.Label, b.Value}
		w = max(w, width(prefix)+width(b.Label)+2+width(b.Value))
	}
	if opt.Cols > 0 && w > opt.Cols {
		// Labels give way; the tree and the values stay.
		w = opt.Cols
		for i := range lines {
			room := w - width(lines[i][0]) - 2 - width(lines[i][2])
			if room < 1 {
				return nil, ErrTooWide
			}
			lines[i][1] = cut(lines[i][1], room)
		}
	}
	titleH := 0
	if len(t.Title) > 0 {
		titleH = len(t.Title) + 1
	}
	c, err := newCanvas(max(w, 1), max(n+titleH, 1))
	if err != nil {
		return nil, err
	}
	for i, line := range t.Title {
		c.text((w-width(line))/2, i, line, roleTitle)
	}
	for i, line := range lines {
		y := titleH + i
		c.text(0, y, line[0], roleLine)
		role := uint8(roleText)
		if t.Tree[i].Section {
			role = roleHead
		}
		x := width(line[0])
		c.text(x, y, line[1], role)
		if line[2] != "" {
			c.text(x+width(line[1])+2, y, line[2], roleNote)
		}
	}
	return c, nil
}
