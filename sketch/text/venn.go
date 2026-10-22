package text

import (
	"math"
	"strconv"
	"strings"

	"tuios.dev/sketch"
	"tuios.dev/sketch/internal/types"
)

// A venn diagram is drawn as its circles, in braille dots, each in its set's series colour, with
// each region's label and texts where the SVG writes them. Without colour, or when the labels do
// not fit inside the circles, it is a table: the sets, then the overlaps the source states, each
// with its label, its relative size, and the texts written in it. When a row does not fit the
// columns, its label and texts go on the lines under its name, and long text wraps.

// vennSize writes a relative size with at most three decimals.
func vennSize(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) >= 1e15 {
		return number(v)
	}
	return strconv.FormatFloat(math.Round(v*1000)/1000, 'f', -1, 64)
}

// piece is a piece of a line of text at a column.
type piece struct {
	x    int
	text string
	role uint8
}

func drawVenn(l *sketch.Layout, opt Options) (*canvas, error) {
	vn := l.Venn
	if vn == nil {
		return nil, ErrUnsupported
	}
	if opt.Color && (opt.Cols == 0 || opt.Cols > 40) {
		if c := vennCircles(vn, opt); c != nil {
			return c, nil
		}
	}
	return vennTable(vn, opt)
}

// vennCircles draws the circles, or gives nil when the labels do not fit.
func vennCircles(vn *types.GeoVenn, opt Options) *canvas {
	if len(vn.Circles) == 0 || len(vn.Circles) > 8 {
		return nil
	}
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, c := range vn.Circles {
		minX, maxX = math.Min(minX, c.X-c.R), math.Max(maxX, c.X+c.R)
		minY, maxY = math.Min(minY, c.Y-c.R), math.Max(maxY, c.Y+c.R)
	}
	if !(maxX > minX) || !(maxY > minY) {
		return nil
	}
	// The circles take 64 columns, fewer when the columns are fewer. A
	// cell is pieAspect times as tall as it is wide.
	cols := 64
	if opt.Cols > 0 {
		cols = min(cols, opt.Cols-2)
	}
	sx := float64(cols) / (maxX - minX)
	sy := sx / pieAspect
	rows := int(math.Ceil((maxY-minY)*sy)) + 1
	if int64(cols+2)*int64(rows+len(vn.Title)+1) > MaxCells {
		return nil
	}
	c, err := newCanvas(cols+2, rows)
	if err != nil {
		return nil
	}
	cellX := func(x float64) float64 { return (x-minX)*sx + 1 }
	cellY := func(y float64) float64 { return (y - minY) * sy }
	// The labels first, where the SVG centres them. One that would leave
	// the canvas, or cover another, means the circles are too small.
	for _, r := range vn.Regions {
		var lines []string
		role := uint8(roleText)
		if r.Label != "" {
			lines = append(lines, r.Label)
		}
		lines = append(lines, r.Texts...)
		if len(lines) == 0 {
			continue
		}
		if len(r.Sets) == 1 {
			role = roleTitle
		}
		y0 := int(math.Round(cellY(r.Y) - float64(len(lines))/2))
		for k, line := range lines {
			w := width(line)
			x := int(math.Round(cellX(r.X) - float64(w)/2))
			y := y0 + k
			if x < 0 || x+w > c.w || y < 0 || y >= c.h || !c.blank(x, y, w) {
				return nil
			}
			lr := role
			if k > 0 || r.Label == "" {
				lr = roleNote
			}
			c.text(x, y, line, lr)
		}
	}
	d := newDotGrid(c.w, c.h)
	d.merge = true
	for i, circle := range vn.Circles {
		l := d.layer(seriesRole(i))
		// In dots: two to a cell across, four down.
		cx, cy := cellX(circle.X)*2, cellY(circle.Y)*4
		rx, ry := circle.R*sx*2, circle.R*sy*4
		steps := int(math.Max(rx, ry)*7) + 16
		for k := 0; k < steps; k++ {
			a := 2 * math.Pi * float64(k) / float64(steps)
			d.set(l, int(math.Round(cx+math.Cos(a)*rx)), int(math.Round(cy+math.Sin(a)*ry)))
		}
	}
	d.paint(c, 0, 0)
	out, err := withTitle(c, vn.Title, opt.Cols)
	if err != nil {
		return nil
	}
	return out
}

func vennTable(vn *types.GeoVenn, opt Options) (*canvas, error) {
	type row struct {
		name, label, size string
		texts             []string
	}
	var sets, overlaps []row
	nameW, labelW, sizeW := 0, 0, len("Size")
	for _, r := range vn.Regions {
		rw := row{name: strings.Join(r.Sets, " ∩ "), label: r.Label, size: vennSize(r.Size), texts: r.Texts}
		if len(r.Sets) == 1 && rw.label == r.Sets[0] {
			rw.label = ""
		}
		nameW = max(nameW, width(rw.name))
		labelW = max(labelW, width(rw.label))
		sizeW = max(sizeW, width(rw.size))
		if len(r.Sets) == 1 {
			sets = append(sets, rw)
		} else {
			overlaps = append(overlaps, rw)
		}
	}
	// A row is two cells of indent, the name, the label and the size, two cells between each.
	labelX := 2 + nameW + 2
	sizeEnd := labelX + sizeW
	if labelW > 0 {
		sizeEnd = labelX + labelW + 2 + sizeW
	}
	inline := opt.Cols <= 0 || sizeEnd <= opt.Cols
	room := 1 << 20
	if opt.Cols > 0 {
		room = opt.Cols
	}
	var lines [][]piece
	wrapped := func(x int, s string, role uint8) {
		for _, part := range wrapCells(s, max(1, room-x), 1000) {
			lines = append(lines, []piece{{x, part, role}})
		}
	}
	for _, t := range vn.Title {
		wrapped(0, t, roleTitle)
	}
	section := func(head string, rows []row) {
		if len(rows) == 0 {
			return
		}
		if inline {
			lines = append(lines, []piece{{0, head, roleHead}, {sizeEnd - len("Size"), "Size", roleHead}})
		} else {
			lines = append(lines, []piece{{0, head, roleHead}})
		}
		for _, r := range rows {
			if inline {
				line := []piece{{2, r.name, roleBox}, {sizeEnd - width(r.size), r.size, roleLine}}
				if r.label != "" {
					line = append(line, piece{labelX, r.label, roleNote})
				}
				lines = append(lines, line)
			} else {
				wrapped(2, r.name+" ("+r.size+")", roleBox)
				if r.label != "" {
					wrapped(4, r.label, roleNote)
				}
			}
			for _, t := range r.texts {
				wrapped(4, "· "+t, roleText)
			}
		}
	}
	section("Sets", sets)
	section("Overlaps", overlaps)
	cw := 0
	for _, line := range lines {
		for _, s := range line {
			cw = max(cw, s.x+width(s.text))
		}
	}
	if opt.Cols > 0 && cw > opt.Cols {
		return nil, ErrTooWide
	}
	c, err := newCanvas(max(cw, 1), max(len(lines), 1))
	if err != nil {
		return nil, err
	}
	for y, line := range lines {
		for _, s := range line {
			c.text(s.x, y, s.text, s.role)
		}
	}
	return c, nil
}
