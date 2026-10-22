package text

import (
	"strings"

	"tuios.dev/sketch"
	"tuios.dev/sketch/internal/types"
)

// A user journey is drawn from its lists: the tasks run across as boxes, under
// the bands of their sections, with a chart of the scores below them. When that
// is wider than the columns, the tasks run down instead, each with its score as
// a row of dots.

func drawJourney(l *sketch.Layout, opt Options) (*canvas, error) {
	j := l.Journey
	if j == nil {
		return nil, ErrUnsupported
	}
	if c, err := journeyAcross(j, opt); err != ErrTooWide {
		return c, err
	}
	return journeyDown(j, opt)
}

// taskLines are the lines inside a task's box: its label, then the people on
// it, as many names to a line as fit in w cells. A name wider than w has a line
// of its own.
func taskLines(t types.GeoStep, w int) []string {
	lines := append([]string{}, t.Label...)
	line := ""
	for _, name := range t.People {
		switch {
		case line == "":
			line = name
		case width(line)+2+width(name) <= w:
			line += ", " + name
		default:
			lines = append(lines, line+",")
			line = name
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// textWidth is the width of the text in a task's box: its label, or 12 cells,
// whichever is wider, so that a few names fit on a line.
func textWidth(t types.GeoStep) int {
	w := max(widest(t.Label), 12)
	for _, name := range t.People {
		w = max(w, width(name)+1)
	}
	return w
}

func journeyAcross(j *types.GeoJourney, opt Options) (*canvas, error) {
	// The scale of the chart takes the first two columns. Each task's score is a
	// dot in the chart, under the middle of its box.
	const gutter = 2
	cols := make([]int, len(j.Tasks))
	lines := make([][]string, len(j.Tasks))
	card := 0
	for i, t := range j.Tasks {
		w := textWidth(t)
		lines[i] = taskLines(t, w)
		cols[i] = w + 4
		card = max(card, len(lines[i])+2)
	}
	band := 0
	for _, s := range j.Sections {
		if len(s.Label) == 0 || len(s.Periods) == 0 {
			continue
		}
		band = max(band, len(s.Label)+2)
		// A band is as wide as its tasks, and its name widens the last of them.
		span := -1
		for _, i := range s.Periods {
			span += cols[i] + 1
		}
		if need := widest(s.Label) + 4; span < need {
			cols[s.Periods[len(s.Periods)-1]] += need - span
		}
	}
	x := make([]int, len(j.Tasks))
	at := gutter
	for i := range j.Tasks {
		x[i] = at
		at += cols[i] + 1
	}
	wd := max(at, widest(j.Title))
	if opt.Cols > 0 && wd > opt.Cols {
		return nil, ErrTooWide
	}
	titleH := 0
	if len(j.Title) > 0 {
		titleH = len(j.Title) + 1
	}
	cardsY := titleH
	if band > 0 {
		cardsY += band
	}
	chartY := cardsY + card + 1
	height := chartY
	if len(j.Tasks) > 0 {
		height += 5
	}
	c, err := newCanvas(wd, max(height, 1))
	if err != nil {
		return nil, err
	}
	for i, line := range j.Title {
		c.text((wd-width(line))/2, i, line, roleTitle)
	}
	for _, s := range j.Sections {
		if len(s.Label) == 0 || len(s.Periods) == 0 {
			continue
		}
		first, last := s.Periods[0], s.Periods[len(s.Periods)-1]
		c.box(x[first], titleH, x[last]+cols[last]-x[first], band, s.Label, roleFrame, roleHead)
	}
	if len(j.Tasks) == 0 {
		return c, nil
	}
	for score := 5; score >= 1; score-- {
		y := chartY + 5 - score
		c.put(0, y, rune('0'+score), roleText)
		c.hline(gutter, at-2, y, dotted, roleLine)
	}
	for i, t := range j.Tasks {
		c.box(x[i], cardsY, cols[i], card, nil, roleBox, roleText)
		for k, line := range lines[i] {
			role := uint8(roleText)
			if k >= len(t.Label) {
				role = roleNote
			}
			c.text(x[i]+2, cardsY+1+k, line, role)
		}
		cx := x[i] + cols[i]/2
		// The score is drawn at the nearest whole level.
		y := chartY + 5 - int(t.Level+0.5)
		c.put(cx, y, '●', roleTitle)
	}
	return c, nil
}

// scoreDots is a score as five dots, as many of them filled as the level.
func scoreDots(level float64) string {
	n := int(level + 0.5)
	return strings.Repeat("●", n) + strings.Repeat("○", 5-n)
}

func journeyDown(j *types.GeoJourney, opt Options) (*canvas, error) {
	// pele wraps a label to the width of a box across. Down the page there
	// is room for a longer line, up to 40 cells, so each label is wrapped
	// again.
	room := 40
	if opt.Cols > 0 {
		room = min(room, opt.Cols-10)
	}
	tasks := make([]types.GeoStep, len(j.Tasks))
	for i, t := range j.Tasks {
		if room >= 12 && len(t.Label) > 1 {
			t.Label = wrap(strings.Join(t.Label, " "), room)
		}
		tasks[i] = t
	}
	jj := *j
	jj.Tasks = tasks
	j = &jj
	boxW := 6
	lines := make([][]string, len(j.Tasks))
	for i, t := range j.Tasks {
		w := textWidth(t)
		lines[i] = taskLines(t, w)
		boxW = max(boxW, w+4)
	}
	// The score is right of the box: a space, then five dots.
	wd := max(boxW+6, widest(j.Title))
	height := 0
	if len(j.Title) > 0 {
		height = len(j.Title) + 1
	}
	for _, s := range j.Sections {
		if len(s.Label) > 0 {
			wd = max(wd, widest(s.Label)+4)
			height += len(s.Label) + 2
		}
		for _, i := range s.Periods {
			height += len(lines[i]) + 2
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
	for i, line := range j.Title {
		c.text((wd-width(line))/2, i, line, roleTitle)
		y = i + 2
	}
	for _, s := range j.Sections {
		if len(s.Label) > 0 {
			c.box(0, y, wd, len(s.Label)+2, s.Label, roleFrame, roleHead)
			y += len(s.Label) + 2
		}
		for _, i := range s.Periods {
			t := j.Tasks[i]
			c.box(0, y, boxW, len(lines[i])+2, nil, roleBox, roleText)
			for k, line := range lines[i] {
				role := uint8(roleText)
				if k >= len(t.Label) {
					role = roleNote
				}
				c.text(2, y+1+k, line, role)
			}
			c.text(boxW+1, y+1, scoreDots(t.Level), roleTitle)
			y += len(lines[i]) + 2
		}
	}
	return c, nil
}
