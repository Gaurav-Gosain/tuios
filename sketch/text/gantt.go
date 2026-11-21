package text

import (
	"math"
	"strings"

	"tuios.dev/plot"
	sketch "tuios.dev/sketch/internal/engine"
	"tuios.dev/sketch/internal/types"
)

var ganttKinds = [...]struct {
	glyph rune
	name  string
	role  uint8
}{
	{'█', "task", roleBox},
	{'▒', "active", roleBox},
	{'░', "done", roleLine},
	{'▓', "critical", roleCrit},
	{'■', "critical, active", roleCrit},
	{'□', "critical, done", roleCrit},
	{'◆', "milestone", roleBox},
	{'·', "excluded", roleFrame},
}

func ganttKind(t *types.GeoTask) int {
	k := 0
	switch {
	case t.Done:
		k = 2
	case t.Active:
		k = 1
	}
	if t.Crit {
		k += 3
	}
	if t.Milestone {
		k = 6
	}
	return k
}

func drawGantt(l *sketch.Layout, opt Options) (*canvas, error) {
	g := l.Gantt
	if g == nil {
		return nil, ErrUnsupported
	}
	if len(g.Rows) == 0 || !(g.Start <= g.Stop) {
		empty := wrap("The chart has no tasks with dates.", opt.Cols)
		c, err := newCanvas(widest(empty), len(empty))
		if err != nil {
			return nil, err
		}
		for y, l := range empty {
			c.text(0, y, l, roleLine)
		}
		return withTitle(c, g.Title, opt.Cols)
	}
	labels := make([][]string, len(g.Rows))
	for _, t := range g.Tasks {
		if t.Row >= 0 && t.Row < len(g.Rows) {
			labels[t.Row] = append(labels[t.Row], t.Label)
		}
	}
	rowLabel := make([]string, len(g.Rows))
	for i, l := range labels {
		rowLabel[i] = strings.Join(l, ", ")
	}
	named := false
	for _, s := range g.Rows {
		named = named || s != g.Rows[0] || s != ""
	}

	labelW := 0
	for i, s := range g.Rows {
		labelW = max(labelW, width(rowLabel[i])+2)
		if named {
			labelW = max(labelW, width(s))
		}
	}
	plotW := 60
	if opt.Cols > 0 {
		labelW = min(labelW, max(opt.Cols*2/5, 10))
		plotW = opt.Cols - labelW - 3
		if plotW < 16 {
			return nil, ErrTooWide
		}
	}
	span := g.Stop - g.Start
	at := func(t float64) float64 {
		if span <= 0 {
			return float64(plotW) / 2
		}
		return (t - g.Start) / span * float64(plotW)
	}
	col := func(t float64) int { return min(max(int(math.Floor(at(t))), 0), plotW-1) }

	type line struct {
		label   string
		section bool
		row     int
	}
	var lines []line
	firstLine := make([]int, len(g.Rows))
	for i, s := range g.Rows {
		if named && (i == 0 || s != g.Rows[i-1]) {
			for _, part := range wrap(s, labelW) {
				lines = append(lines, line{label: part, section: true, row: -1})
			}
		}
		if i > 0 && len(lines) > 0 && !lines[len(lines)-1].section {
			lines = append(lines, line{row: -1})
		}
		firstLine[i] = len(lines)
		parts := wrap(rowLabel[i], labelW-2)
		for k, part := range parts {
			row := -1
			if k == 0 {
				row = i
			}
			lines = append(lines, line{label: part, row: row})
		}
	}
	axis := len(lines)
	x0 := labelW + 2
	w := x0 + plotW + 1
	var used [len(ganttKinds)]bool
	for i := range g.Tasks {
		if g.Tasks[i].Scheduled {
			used[ganttKind(&g.Tasks[i])] = true
		}
	}
	used[7] = len(g.Excluded) > 0
	var legend [][]int
	lineW := 0
	for k, u := range used {
		if !u {
			continue
		}
		iw := 2 + width(ganttKinds[k].name)
		if len(legend) == 0 || lineW+2+iw > w {
			legend = append(legend, nil)
			lineW = -2
		}
		legend[len(legend)-1] = append(legend[len(legend)-1], k)
		lineW += 2 + iw
	}
	if len(legend) == 1 && len(legend[0]) == 1 && legend[0][0] == 0 {
		legend = nil
	}
	h := axis + 2 + len(g.Verts)
	if len(legend) > 0 {
		h += 1 + len(legend)
	}
	c, err := newCanvas(w, h)
	if err != nil {
		return nil, err
	}

	type tick struct {
		x, at int
		label string
	}
	var ticks []tick
	right := -2
	last := ""
	for _, t := range g.Ticks {
		if t.T < g.Start || t.T > g.Stop {
			continue
		}
		x := x0 + col(t.T)
		at := min(max(x-width(t.Label)/2, 0), w-width(t.Label))
		if at <= right+1 || t.Label == last {
			continue
		}
		ticks = append(ticks, tick{x, at, t.Label})
		right, last = at+width(t.Label)-1, t.Label
	}
	for _, t := range ticks {
		c.vline(t.x, 0, axis, dotted, roleFrame)
	}
	c.hline(x0, x0+plotW-1, axis, light, roleFrame)
	for _, t := range ticks {
		c.stroke(t.x, axis, down, light, roleFrame)
		c.text(t.at, axis+1, t.label, roleLine)
	}
	for _, ex := range g.Excluded {
		a := col(ex[0])
		b := min(max(int(math.Ceil(at(ex[1])))-1, a), plotW-1)
		for y := 0; y < axis; y++ {
			for x := a; x <= b; x++ {
				if p := c.at(x0+x, y); p != nil && p.r == 0 && p.mask == 0 {
					c.put(x0+x, y, '·', roleFrame)
				}
			}
		}
	}
	if !math.IsNaN(g.Today) {
		x := x0 + col(g.Today)
		for y := 0; y < axis; y++ {
			c.put(x, y, '│', roleCrit)
		}
	}
	for _, v := range g.Verts {
		x := x0 + col(v.T)
		for y := 0; y < axis; y++ {
			c.put(x, y, '┃', roleNote)
		}
	}

	for y, ln := range lines {
		if ln.section {
			c.text(0, y, ln.label, roleTitle)
			continue
		}
		c.text(2, y, ln.label, roleText)
	}
	for i := range g.Tasks {
		t := &g.Tasks[i]
		if !t.Scheduled || t.Row < 0 || t.Row >= len(g.Rows) {
			continue
		}
		y := firstLine[t.Row]
		kind := ganttKinds[ganttKind(t)]
		glyph, role := kind.glyph, kind.role
		if t.Milestone {
			if t.Crit {
				role = roleCrit
			}
			c.put(x0+col((t.Start+t.End)/2), y, '◆', role)
			continue
		}
		bar := plot.Span{Start: t.Start, End: t.End, Min: g.Start, Max: g.Stop, Fill: string(glyph)}.Draw(plotW, plot.Blocks)
		for x := 0; x < bar.W; x++ {
			if cell := bar.At(x, 0); cell.Text != "" {
				c.put(x0+x, y, glyph, role)
			}
		}
	}

	for k, v := range g.Verts {
		x := x0 + col(v.T)
		s := "┃ " + v.Label
		if x+width(s) > w {
			s = v.Label + " ┃"
			x = max(x-width(s)+1, 0)
		}
		c.text(x, axis+2+k, s, roleNote)
	}
	for k, ln := range legend {
		y := axis + 3 + len(g.Verts) + k
		x := 0
		for _, i := range ln {
			kind := ganttKinds[i]
			c.put(x, y, kind.glyph, kind.role)
			c.text(x+2, y, kind.name, roleLine)
			x += 4 + width(kind.name)
		}
	}
	return withTitle(c, g.Title, opt.Cols)
}
