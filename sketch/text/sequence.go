package text

import (
	"math"

	"tuios.dev/sketch"
)

func drawSequence(l *sketch.Layout, opt Options) (*canvas, error) {
	sx := 1.0 / 9
	sy := 0.0
	for _, a := range l.Actors {
		if a.W > 0 {
			sx = math.Max(sx, float64(widest(a.Label)+4)/a.W)
		}
		if a.H > 0 {
			sy = math.Max(sy, float64(len(a.Label)+2)/a.H)
		}
	}
	for _, m := range l.Messages {
		if w := widest(m.Label); w > 0 && !m.Self && math.Abs(m.X1-m.X0) > 0 {
			sx = math.Max(sx, float64(w+4)/math.Abs(m.X1-m.X0))
		}
		if n := len(m.Label); n > 0 && !m.Self {
			// The label sits above the line, from its top to 4 units above the line.
			top := m.LabelY - (m.Y - 4 - m.LabelY)
			if span := m.Y - top; span > 0 {
				sy = math.Max(sy, float64(n+1)/span)
			}
		}
	}
	// A self message's label goes right of its loop, and must end before the next lifeline.
	for _, m := range l.Messages {
		if !m.Self || widest(m.Label) == 0 {
			continue
		}
		from := l.Actors[m.From].X
		next := math.Inf(1)
		for _, a := range l.Actors {
			if a.X > from+1 {
				next = math.Min(next, a.X)
			}
		}
		if !math.IsInf(next, 1) {
			sx = math.Max(sx, float64(widest(m.Label)+8)/(next-from))
		}
	}
	for _, n := range l.Notes {
		if n.W > 0 {
			sx = math.Max(sx, float64(widest(n.Label)+4)/n.W)
		}
		if n.H > 0 {
			sy = math.Max(sy, float64(len(n.Label)+2)/n.H)
		}
	}
	sy = math.Max(sy, sx*0.45)
	col := func(x float64) int { return int(math.Round(x * sx)) }
	row := func(y float64) int { return int(math.Round(y * sy)) }

	head := make([]rect, len(l.Actors))
	foot := make([]rect, len(l.Actors))
	box := func(cx int, top float64, w, h float64, label []string) rect {
		bw := max(col(w), widest(label)+4)
		bh := len(label) + 2
		return rect{cx - bw/2, row(top), cx - bw/2 + bw - 1, row(top) + bh - 1}
	}
	for i, a := range l.Actors {
		head[i] = box(col(a.X), a.Top, a.W, a.H, a.Label)
		if a.Foot >= 0 {
			foot[i] = box(col(a.X), a.Foot, a.W, a.H, a.Label)
		}
	}
	// A foot box starts below the last thing drawn above it.
	lastRow := 0
	for i := range l.Actors {
		lastRow = max(lastRow, head[i].y1)
	}
	for _, m := range l.Messages {
		lastRow = max(lastRow, row(m.Y), row(m.Y2))
	}
	for _, n := range l.Notes {
		lastRow = max(lastRow, row(n.Y)+max(row(n.H), len(n.Label)+2)-1)
	}
	for _, b := range l.Blocks {
		lastRow = max(lastRow, row(b.Bottom))
	}
	for i, a := range l.Actors {
		if a.Foot >= 0 && foot[i].y0 <= lastRow+1 {
			h := foot[i].y1 - foot[i].y0
			foot[i].y0 = lastRow + 2
			foot[i].y1 = foot[i].y0 + h
		}
	}

	all := rect{0, 0, col(l.Width), 0}
	for i, a := range l.Actors {
		all = all.union(head[i])
		if a.Foot >= 0 {
			all = all.union(foot[i])
		}
	}
	for _, m := range l.Messages {
		all = all.union(rect{col(m.X0), row(m.Y), col(m.X1), row(m.Y)})
		if w := widest(m.Label); w > 0 {
			if m.Self {
				all = all.union(rect{col(m.X0), row(m.Y), col(math.Max(m.X0, m.X1)) + 4 + w, row(m.Y2)})
			} else {
				all = all.union(rect{col(m.LabelX) - w/2 - 1, row(m.Y) - len(m.Label), col(m.LabelX) + w/2 + 1, row(m.Y)})
			}
		}
	}
	notes := make([]rect, len(l.Notes))
	for i, n := range l.Notes {
		w := max(col(n.W), widest(n.Label)+4)
		h := max(row(n.H), len(n.Label)+2)
		cx := col(n.X + n.W/2)
		notes[i] = rect{cx - w/2, row(n.Y), cx - w/2 + w - 1, row(n.Y) + h - 1}
		all = all.union(notes[i])
	}
	for _, b := range l.Blocks {
		all = all.union(rect{col(b.X0), row(b.Top), col(b.X1), row(b.Bottom)})
	}
	dx, dy := -all.x0, -all.y0
	cw, ch := all.x1-all.x0+1, all.y1-all.y0+2
	if opt.Cols > 0 && cw > opt.Cols {
		return nil, ErrTooWide
	}
	c, err := newCanvas(cw, ch)
	if err != nil {
		return nil, err
	}
	move := func(r rect) rect { return rect{r.x0 + dx, r.y0 + dy, r.x1 + dx, r.y1 + dy} }

	// Blocks, as frames that lifelines and messages cross.
	for _, b := range l.Blocks {
		r := move(rect{col(b.X0), row(b.Top), col(b.X1), row(b.Bottom)})
		c.hline(r.x0, r.x1, r.y0, light, roleFrame)
		c.hline(r.x0, r.x1, r.y1, light, roleFrame)
		c.vline(r.x0, r.y0, r.y1, light, roleFrame)
		c.vline(r.x1, r.y0, r.y1, light, roleFrame)
		for _, d := range b.Dividers {
			c.hline(r.x0, r.x1, row(d)+dy, dotted, roleFrame)
		}
	}
	// Lifelines.
	for i, a := range l.Actors {
		x := col(a.X) + dx
		end := row(a.LifeEnd) + dy
		if a.Foot >= 0 {
			end = foot[i].y0 + dy - 1
		}
		c.vline(x, head[i].y1+dy+1, end, light, roleLine)
	}
	// Messages.
	type mark struct {
		x, y int
		r    rune
	}
	var heads []mark
	for _, m := range l.Messages {
		weight := uint8(light)
		if m.Dotted {
			weight = dotted
		}
		y := row(m.Y) + dy
		a, b := col(m.X0)+dx, col(m.X1)+dx
		if m.Self {
			out := max(a, b) + 3
			y2 := max(row(m.Y2)+dy, y+1)
			c.hline(a, out, y, weight, roleLine)
			c.vline(out, y, y2, weight, roleLine)
			c.hline(b+1, out, y2, weight, roleLine)
			if m.HeadEnd {
				heads = append(heads, mark{b + 1, y2, '◄'})
			}
			if m.HeadStart {
				heads = append(heads, mark{a + 1, y, '◄'})
			}
			continue
		}
		dir := 1
		if b < a {
			dir = -1
		}
		end := b
		if m.HeadEnd {
			end = b - dir
		}
		start := a
		if m.HeadStart {
			start = a + dir
		}
		c.hline(start, end, y, weight, roleLine)
		if m.HeadEnd {
			heads = append(heads, mark{end, y, map[int]rune{1: '►', -1: '◄'}[dir]})
		}
		if m.HeadStart {
			heads = append(heads, mark{start, y, map[int]rune{1: '◄', -1: '►'}[dir]})
		}
	}
	// Notes and participants.
	for i, n := range l.Notes {
		r := move(notes[i])
		drawNode(c, r, "rect", n.Label)
		for y := r.y0; y <= r.y1; y++ {
			for x := r.x0; x <= r.x1; x++ {
				if p := c.at(x, y); p != nil && p.role == roleBox {
					p.role = roleNote
				}
			}
		}
	}
	for i, a := range l.Actors {
		drawNode(c, move(head[i]), "rect", a.Label)
		if a.Foot >= 0 {
			drawNode(c, move(foot[i]), "rect", a.Label)
		}
	}
	// Labels and heads.
	for _, m := range l.Messages {
		if len(m.Label) == 0 || widest(m.Label) == 0 {
			continue
		}
		if m.Self {
			y := (row(m.Y) + max(row(m.Y2), row(m.Y)+1)) / 2
			c.text(max(col(m.X0), col(m.X1))+dx+5, y+dy, m.Label[0], roleText)
			for k := 1; k < len(m.Label); k++ {
				c.text(max(col(m.X0), col(m.X1))+dx+5, y+dy+k, m.Label[k], roleText)
			}
			continue
		}
		y := row(m.Y) + dy - len(m.Label)
		for k, line := range m.Label {
			w := width(line)
			x := col(m.LabelX) + dx - w/2
			c.text(x, y+k, line, roleText)
		}
	}
	for _, h := range heads {
		c.put(h.x, h.y, h.r, roleHead)
	}
	for _, b := range l.Blocks {
		r := move(rect{col(b.X0), row(b.Top), col(b.X1), row(b.Bottom)})
		title := " " + b.Kind
		if len(b.Title) > 0 && b.Title[0] != "" {
			title += " " + b.Title[0]
		}
		c.text(r.x0+1, r.y0, title+" ", roleTitle)
		for k, d := range b.Dividers {
			if k < len(b.DividerLabels) && len(b.DividerLabels[k]) > 0 && b.DividerLabels[k][0] != "" {
				c.text(r.x0+2, row(d)+dy, " "+b.DividerLabels[k][0]+" ", roleFrame)
			}
		}
	}
	return c, nil
}
