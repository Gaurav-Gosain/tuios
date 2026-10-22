package text

import (
	"strings"

	"tuios.dev/sketch"
	"tuios.dev/sketch/internal/types"
)

// An event model is drawn as its swimlanes: one band a lane, named at the left, and the frames
// as boxes in time order from left to right, each with its frame number on its top edge. A frame
// in a new lane starts under the end of the one before it, as in the SVG, so the arrow between
// them drops straight down. Notes and specifications follow as boxes under the lanes. When the
// lanes do not fit the columns, they are drawn tighter: each lane's name goes on its rule, a
// frame in a new lane starts a few cells after the one before it, and data wraps narrower. When that does not fit either, the
// frames are listed one under another with their lanes.

const emDataWidth = 28

// emBox is a frame's box in cells.
type emBox struct {
	x, y, w, h int
	lines      []styled
}

// emLines are the lines inside a frame's box: its name and its data, each at most w cells.
func emLines(f types.GeoFrame, w int) []styled {
	out := []styled{}
	for _, part := range wrapCells(f.Name, w, 1000) {
		out = append(out, styled{part, roleTitle})
	}
	if len(out) == 0 {
		out = append(out, styled{"", roleTitle})
	}
	for _, line := range f.Data {
		for _, part := range wrapCells(line, w, 1000) {
			out = append(out, styled{part, roleNote})
		}
	}
	return out
}

// emRole is the colour of a frame's box by its kind.
func emRole(kind string) uint8 {
	switch kind {
	case "event":
		return roleNote
	case "ui":
		return roleFrame
	}
	return roleBox
}

// drawEmBox draws a box with its frame number on the top edge.
func drawEmBox(c *canvas, b emBox, f types.GeoFrame) {
	role := emRole(f.Kind)
	c.box(b.x, b.y, b.w, b.h, nil, role, roleText)
	id := " " + f.ID + " "
	if f.Reset {
		id = " " + f.ID + " ↺ "
	}
	if width(id)+2 <= b.w {
		c.text(b.x+1, b.y, id, role)
	}
	for k, line := range b.lines {
		c.text(b.x+2, b.y+1+k, line.text, line.role)
	}
}

func drawEventModel(l *sketch.Layout, opt Options) (*canvas, error) {
	em := l.EventModel
	if em == nil {
		return nil, ErrUnsupported
	}
	if c := emLanes(em, opt, false); c != nil {
		return c, nil
	}
	if c := emLanes(em, opt, true); c != nil {
		return c, nil
	}
	return emList(em, opt)
}

// emCards are the notes and specifications as boxes at most w cells wide.
func emCards(em *types.GeoEventModel, w int) [][]styled {
	var out [][]styled
	for _, card := range em.Notes {
		var lines []styled
		for _, part := range wrapCells(card.Head, w, 1000) {
			lines = append(lines, styled{part, roleTitle})
		}
		for _, line := range card.Lines {
			for _, part := range wrapCells(line, w, 1000) {
				lines = append(lines, styled{part, roleText})
			}
			if line == "" {
				lines = append(lines, styled{})
			}
		}
		out = append(out, lines)
	}
	return out
}

func emLanes(em *types.GeoEventModel, opt Options, tight bool) *canvas {
	dataW := emDataWidth
	if tight {
		dataW = 16
	}
	labelW := 0
	for _, lane := range em.Lanes {
		labelW = max(labelW, width(lane))
	}
	labelW += 2
	boxes := make([]emBox, len(em.Boxes))
	laneRight := make([]int, len(em.Lanes))
	laneH := make([]int, len(em.Lanes))
	for i := range laneRight {
		laneRight[i] = -1 << 30
	}
	right := 0
	for i, f := range em.Boxes {
		lines := emLines(f, dataW)
		w := max(styledWidth(lines)+4, width(f.ID)+6, 10)
		h := len(lines) + 2
		x := 0
		if i > 0 {
			prev := boxes[i-1]
			if em.Boxes[i-1].Lane == f.Lane {
				x = laneRight[f.Lane] + 3
			} else if tight {
				// Far enough along for the arrow from the frame before to
				// drop straight down, and no further.
				x = max(prev.x+4, laneRight[f.Lane]+3)
			} else {
				x = max(prev.x+max(prev.w/2, prev.w-8), laneRight[f.Lane]+3)
			}
		}
		boxes[i] = emBox{x: x, w: w, h: h, lines: lines}
		laneRight[f.Lane] = x + w
		laneH[f.Lane] = max(laneH[f.Lane], h)
		right = max(right, x+w)
	}
	// Each lane is a blank row, its boxes, and a blank row, under a rule.
	left := labelW + 1
	if tight {
		// The lane's name goes on its rule, so the boxes start at the left.
		left = 1
	}
	w := left + right + 1
	laneY := make([]int, len(em.Lanes))
	y := len(em.Title)
	for i := range em.Lanes {
		laneY[i] = y
		y += 1 + 1 + laneH[i] + 1
	}
	rule := y
	cardW := min(max(w-4, 40), 80)
	cards := emCards(em, cardW)
	h := rule + 1
	for _, card := range cards {
		h += 1 + len(card) + 2
		w = max(w, styledWidth(card)+4)
	}
	w = max(w, widest(em.Title))
	if (opt.Cols > 0 && w > opt.Cols) || int64(w)*int64(h) > MaxCells {
		return nil
	}
	c, err := newCanvas(w, h)
	if err != nil {
		return nil
	}
	for k, line := range em.Title {
		c.text((w-width(line))/2, k, line, roleTitle)
	}
	for i, lane := range em.Lanes {
		c.hline(0, w-1, laneY[i], dotted, roleFrame)
		if tight {
			c.text(2, laneY[i], " "+lane+" ", roleHead)
		} else {
			c.text(0, laneY[i]+2, lane, roleHead)
		}
	}
	if len(em.Lanes) > 0 {
		c.hline(0, w-1, rule, dotted, roleFrame)
	}
	for i := range boxes {
		boxes[i].x += left
		boxes[i].y = laneY[em.Boxes[i].Lane] + 2
	}
	// Arrows go first, so the boxes draw over a line that passes behind them.
	for _, rel := range em.Relations {
		s, t := boxes[rel[0]], boxes[rel[1]]
		if rel[0] == rel[1] {
			continue
		}
		if em.Boxes[rel[0]].Lane == em.Boxes[rel[1]].Lane {
			sy := s.y + 1
			if t.x >= s.x+s.w {
				c.hline(s.x+s.w, t.x-1, sy, light, roleLine)
				c.put(t.x-1, sy, '▶', roleLine)
			} else if t.x+t.w <= s.x {
				c.hline(t.x+t.w, s.x-1, sy, light, roleLine)
				c.put(t.x+t.w, sy, '◀', roleLine)
			}
			continue
		}
		from, to := max(s.x, t.x), min(s.x+s.w, t.x+t.w)
		var sx, tx int
		if to-from >= 3 {
			sx = (from + to) / 2
			tx = sx
		} else if t.x > s.x {
			sx, tx = s.x+s.w-2, t.x+1
		} else {
			sx, tx = s.x+1, t.x+t.w-2
		}
		down := t.y > s.y
		sy, ty, head := s.y-1, t.y+t.h, '▲'
		if down {
			sy, ty, head = s.y+s.h, t.y-1, '▼'
		}
		// An S: along the source's column to the blank row beside the target's boxes, across,
		// and into the target.
		lane := em.Boxes[rel[1]].Lane
		turn := laneY[lane] + 1
		if !down {
			turn = laneY[lane] + 2 + laneH[lane]
		}
		if sx == tx {
			c.vline(sx, sy, ty, light, roleLine)
		} else {
			c.vline(sx, sy, turn, light, roleLine)
			c.hline(sx, tx, turn, light, roleLine)
			c.vline(tx, turn, ty, light, roleLine)
		}
		c.put(tx, ty, head, roleLine)
	}
	for i, b := range boxes {
		for yy := b.y; yy < b.y+b.h; yy++ {
			for xx := b.x; xx < b.x+b.w; xx++ {
				c.clear(xx, yy)
			}
		}
		drawEmBox(c, b, em.Boxes[i])
	}
	y = rule + 2
	for _, card := range cards {
		bw := styledWidth(card) + 4
		c.box(0, y, bw, len(card)+2, nil, roleFrame, roleText)
		for k, line := range card {
			c.text(2, y+1+k, line.text, line.role)
		}
		y += len(card) + 3
	}
	return c
}

// emList lists the frames one under another, each under the name of its lane when the lane
// changes. An arrow joins a frame to the next one it feeds; other sources are named in the box.
func emList(em *types.GeoEventModel, opt Options) (*canvas, error) {
	w := 1 << 20
	if opt.Cols > 0 {
		w = opt.Cols
	}
	inner := min(w-4, 40)
	if inner < 4 {
		return nil, ErrTooWide
	}
	feeds := make([]bool, len(em.Boxes))
	sources := make([][]string, len(em.Boxes))
	for _, rel := range em.Relations {
		s, t := rel[0], rel[1]
		if s == t {
			continue
		}
		if t == s+1 {
			feeds[s] = true
			continue
		}
		sources[t] = append(sources[t], em.Boxes[s].ID)
	}
	title := em.Title
	if widest(title) > w {
		title = wrapCells(strings.Join(title, " "), w, 1000)
	}
	type item struct {
		lane  string
		box   []styled
		arrow bool
	}
	var items []item
	h := len(title)
	cw := widest(title)
	for i, f := range em.Boxes {
		it := item{arrow: feeds[i]}
		if i == 0 || em.Boxes[i-1].Lane != f.Lane {
			if f.Lane >= 0 && f.Lane < len(em.Lanes) {
				it.lane = em.Lanes[f.Lane]
				for _, part := range wrapCells(it.lane, w, 1000) {
					cw = max(cw, width(part))
					h++
				}
			}
		}
		it.box = emLines(f, inner)
		if len(sources[i]) > 0 {
			for _, part := range wrapCells("from "+strings.Join(sources[i], ", "), inner, 1000) {
				it.box = append(it.box, styled{part, roleLine})
			}
		}
		cw = max(cw, styledWidth(it.box)+4, width(f.ID)+6)
		h += len(it.box) + 2
		if it.arrow {
			h++
		}
		items = append(items, it)
	}
	cards := emCards(em, inner)
	for _, card := range cards {
		h += 1 + len(card) + 2
		cw = max(cw, styledWidth(card)+4)
	}
	if cw > w {
		return nil, ErrTooWide
	}
	c, err := newCanvas(max(cw, 1), max(h, 1))
	if err != nil {
		return nil, err
	}
	y := 0
	for _, line := range title {
		c.text(0, y, line, roleTitle)
		y++
	}
	for i, it := range items {
		if it.lane != "" {
			for _, part := range wrapCells(it.lane, w, 1000) {
				c.text(0, y, part, roleHead)
				y++
			}
		}
		b := emBox{x: 0, y: y, w: max(styledWidth(it.box)+4, width(em.Boxes[i].ID)+6), h: len(it.box) + 2, lines: it.box}
		drawEmBox(c, b, em.Boxes[i])
		y += b.h
		if it.arrow {
			c.put(2, y, '▼', roleLine)
			y++
		}
	}
	for _, card := range cards {
		y++
		bw := styledWidth(card) + 4
		c.box(0, y, bw, len(card)+2, nil, roleFrame, roleText)
		for k, line := range card {
			c.text(2, y+1+k, line.text, line.role)
		}
		y += len(card) + 2
	}
	return c, nil
}
