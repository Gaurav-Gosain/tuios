package text

import (
	"tuios.dev/sketch"
	"tuios.dev/sketch/internal/types"
)

// A timeline is drawn from its lists, not from the places of the SVG: the
// periods run across as boxes on an axis, with their events in boxes below.
// When that is wider than the columns, the periods run down instead, each
// with its events in a list beside it.

func drawTimeline(l *sketch.Layout, opt Options) (*canvas, error) {
	tl := l.Timeline
	if tl == nil {
		return nil, ErrUnsupported
	}
	if c, err := timelineAcross(tl, opt); err != ErrTooWide {
		return c, err
	}
	return timelineDown(tl, opt)
}

// box draws a box with its top left corner at (x, y), w by h cells, and the
// lines of label inside it from the second column.
func (c *canvas) box(x, y, w, h int, label []string, role, textRole uint8) {
	c.hline(x, x+w-1, y, light, role)
	c.hline(x, x+w-1, y+h-1, light, role)
	c.vline(x, y, y+h-1, light, role)
	c.vline(x+w-1, y, y+h-1, light, role)
	for i, line := range label {
		c.text(x+2, y+1+i, line, textRole)
	}
}

func timelineAcross(tl *types.GeoTimeline, opt Options) (*canvas, error) {
	// Each period is a column as wide as its widest label, period or event.
	cols := make([]int, len(tl.Periods))
	chip := 0
	for i, p := range tl.Periods {
		cols[i] = widest(p.Label) + 4
		for _, e := range p.Events {
			cols[i] = max(cols[i], widest(e)+4)
		}
		chip = max(chip, len(p.Label)+2)
	}
	header := 0
	for _, g := range tl.Groups {
		if len(g.Label) == 0 {
			continue
		}
		header = max(header, len(g.Label)+2)
		// A section is as wide as its periods, and its name widens the last of them.
		span := -1
		for _, i := range g.Periods {
			span += cols[i] + 1
		}
		if need := widest(g.Label) + 4; len(g.Periods) > 0 && span < need {
			cols[g.Periods[len(g.Periods)-1]] += need - span
		}
	}
	// The x of each column, in the order the groups draw them. A section with no
	// periods takes a column of its own.
	x := make([]int, len(tl.Periods))
	groupX := make([]int, len(tl.Groups))
	groupW := make([]int, len(tl.Groups))
	at := 0
	for g, group := range tl.Groups {
		groupX[g] = at
		if len(group.Periods) == 0 {
			at += max(widest(group.Label)+4, 6) + 1
		}
		for _, i := range group.Periods {
			x[i] = at
			at += cols[i] + 1
		}
		groupW[g] = at - 1 - groupX[g]
	}
	wd := max(at+2, widest(tl.Title))
	if opt.Cols > 0 && wd > opt.Cols {
		return nil, ErrTooWide
	}

	titleH := 0
	if len(tl.Title) > 0 {
		titleH = len(tl.Title) + 1
	}
	headerY := titleH
	periodsY := headerY
	if header > 0 {
		periodsY += header + 1
	}
	axisY := periodsY + chip + 1
	eventsY := axisY + 2
	height := axisY + 1
	for _, p := range tl.Periods {
		y := eventsY
		for _, e := range p.Events {
			y += len(e) + 3
		}
		height = max(height, y)
	}
	c, err := newCanvas(wd, height)
	if err != nil {
		return nil, err
	}
	for i, line := range tl.Title {
		c.text((wd-width(line))/2, i, line, roleTitle)
	}
	for g, group := range tl.Groups {
		if len(group.Label) > 0 {
			c.box(groupX[g], headerY, groupW[g], header, group.Label, roleFrame, roleHead)
		}
	}
	if len(tl.Periods) > 0 {
		c.hline(0, at, axisY, light, roleLine)
		c.put(at+1, axisY, '▶', roleLine)
	}
	for i, p := range tl.Periods {
		cx := x[i] + cols[i]/2
		c.vline(cx, periodsY+chip-1, axisY, light, roleLine)
		c.box(x[i], periodsY, cols[i], chip, p.Label, roleBox, roleText)
		c.put(cx, axisY, '●', roleLine)
		y := eventsY
		for _, e := range p.Events {
			c.vline(cx, y-2, y, light, roleLine)
			c.box(x[i], y, cols[i], len(e)+2, e, roleNote, roleText)
			y += len(e) + 3
		}
	}
	return c, nil
}

func timelineDown(tl *types.GeoTimeline, opt Options) (*canvas, error) {
	// Periods in one column of boxes, each with its events listed right of it.
	boxW := 6
	events := 0
	for _, p := range tl.Periods {
		boxW = max(boxW, widest(p.Label)+4)
		for _, e := range p.Events {
			events = max(events, widest(e))
		}
	}
	// The events hang from a line at column sx and start at sx+3.
	sx := boxW + 1
	wd := boxW
	if events > 0 {
		wd = sx + 3 + events
	}
	wd = max(wd, widest(tl.Title))
	rows := func(p types.GeoPeriod) int {
		n := 1
		for _, e := range p.Events {
			n += len(e)
		}
		return max(len(p.Label)+2, n)
	}
	height := 0
	if len(tl.Title) > 0 {
		height = len(tl.Title) + 1
	}
	for _, g := range tl.Groups {
		if len(g.Label) > 0 {
			wd = max(wd, widest(g.Label)+4)
			height += len(g.Label) + 3
		}
		for _, i := range g.Periods {
			height += rows(tl.Periods[i]) + 1
		}
	}
	if opt.Cols > 0 && wd > opt.Cols {
		return nil, ErrTooWide
	}
	c, err := newCanvas(wd, max(height, 1))
	if err != nil {
		return nil, err
	}
	y := 0
	for i, line := range tl.Title {
		c.text((wd-width(line))/2, i, line, roleTitle)
		y = i + 2
	}
	for _, g := range tl.Groups {
		if len(g.Label) > 0 {
			c.box(0, y, wd, len(g.Label)+2, g.Label, roleFrame, roleHead)
			y += len(g.Label) + 3
		}
		for _, i := range g.Periods {
			p := tl.Periods[i]
			c.box(0, y, boxW, len(p.Label)+2, p.Label, roleBox, roleText)
			ey := y + 1
			last := ey
			for _, e := range p.Events {
				last = ey
				c.hline(sx, sx+1, ey, light, roleLine)
				for j, line := range e {
					c.text(sx+3, ey+j, line, roleText)
				}
				ey += len(e)
			}
			if len(p.Events) > 0 {
				c.hline(boxW-1, sx, y+1, light, roleLine)
				c.vline(sx, y+1, last, light, roleLine)
			}
			y += rows(p) + 1
		}
	}
	return c, nil
}
