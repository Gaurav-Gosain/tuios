package text

import (
	"math"

	"tuios.dev/plot"
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
	// The boxes are tuios.dev/plot's treemap, placed as in the SVG.
	pt := plot.Treemap{Boxes: make([]plot.TreemapBox, len(t.Boxes))}
	for i, b := range t.Boxes {
		pt.Boxes[i] = plot.TreemapBox{X: b.X / t.Width, Y: b.Y / t.Height, W: b.W / t.Width, H: b.H / t.Height, Label: safeString(b.Label), Value: safeString(b.Value), Section: b.Section, Depth: b.Depth}
	}
	if !pt.Fits(w, h) {
		return nil
	}
	titleH := 0
	if len(t.Title) > 0 {
		titleH = len(t.Title) + 1
	}
	cw := max(w, widest(t.Title))
	c, err := newCanvas(cw, h+titleH)
	if err != nil {
		return nil
	}
	for i, line := range t.Title {
		c.text((cw-width(line))/2, i, line, roleTitle)
	}
	c.paste(pt.Draw(w, h, plot.Blocks), 0, titleH)
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
