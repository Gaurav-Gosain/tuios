package text

import (
	"math"

	"tuios.dev/sketch"
)

// Gantt charts: each section is a group of rows under its name, and each task
// a bar on a date axis with tick labels under it. The bar's shade says its
// state: █ a task, ▒ an active one, ░ a done one, ▓ a critical one, which is
// red too. A milestone is ◆. Vertical markers and the today line run down
// the chart.

func drawGantt(l *sketch.Layout, opt Options) (*canvas, error) {
	g := l.Gantt
	if g == nil {
		return nil, ErrUnsupported
	}
	if len(g.Rows) == 0 || !(g.Start <= g.Stop) {
		// No task has dates: pele draws the title alone. A line says so.
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
	// The labels of each row, and where sections start.
	rowLabel := make([]string, len(g.Rows))
	for _, t := range g.Tasks {
		if t.Row < 0 || t.Row >= len(g.Rows) {
			continue
		}
		if rowLabel[t.Row] != "" {
			rowLabel[t.Row] += ", "
		}
		rowLabel[t.Row] += t.Label
	}
	named := false
	for _, s := range g.Rows {
		named = named || s != g.Rows[0] || s != ""
	}

	// Columns: the labels, two cells, the plot, one cell.
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
	// at is where a time falls on the plot, in cells.
	at := func(t float64) float64 {
		if span <= 0 {
			return float64(plotW) / 2
		}
		return (t - g.Start) / span * float64(plotW)
	}
	col := func(t float64) int { return min(max(int(math.Floor(at(t))), 0), plotW-1) }

	// The lines of the chart: a section name, or a row of tasks with its label
	// wrapped under the first line.
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
	// Then the axis, the tick labels and the labels of the vertical markers.
	axis := len(lines)
	h := axis + 2 + len(g.Verts)
	x0 := labelW + 2
	w := x0 + plotW + 1
	c, err := newCanvas(w, h)
	if err != nil {
		return nil, err
	}

	// Tick labels, each centred on its tick, skipped when it would touch the
	// one before it or repeat it. The grid runs up from the labelled ticks.
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

	// Labels and bars.
	for y, ln := range lines {
		if ln.section {
			c.text(0, y, ln.label, roleTitle)
			continue
		}
		c.text(2, y, ln.label, roleText)
	}
	for _, t := range g.Tasks {
		if !t.Scheduled || t.Row < 0 || t.Row >= len(g.Rows) {
			continue
		}
		y := firstLine[t.Row]
		role := uint8(roleBox)
		glyph := '█'
		switch {
		case t.Done:
			glyph, role = '░', roleLine
		case t.Active:
			glyph = '▒'
		case t.Crit:
			glyph = '▓'
		}
		if t.Crit {
			role = roleCrit
		}
		if t.Milestone {
			c.put(x0+col((t.Start+t.End)/2), y, '◆', role)
			continue
		}
		// A bar covers every cell it reaches into, and at least one.
		a := col(t.Start)
		b := min(max(int(math.Ceil(at(t.End)))-1, a), plotW-1)
		for x := a; x <= b; x++ {
			c.put(x0+x, y, glyph, role)
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
	return withTitle(c, g.Title, opt.Cols)
}
