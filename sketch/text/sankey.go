package text

import (
	"errors"
	"math"
	"sort"
	"strings"

	"tuios.dev/sketch"
	"tuios.dev/sketch/internal/types"
)

// A sankey diagram is drawn from its lists. The nodes are boxes in columns, one
// column per layer of the layout, in the order the layout stacks them. Each
// flow is a line from the right side of its source to an arrow at the left
// side of its target, through a lane of its own between the columns, with its
// value on it. A flow that passes a column runs straight through it.
//
// Lines that cross are hard to follow in cells. When the lines would cross more
// than maxCrossings times, or the columns do not fit, the diagram is a table
// instead: source, target and value, a group of rows for each source.

// maxCrossings is the most places where two flows cross that a drawing of
// columns may have.
const maxCrossings = 3

var errCrossed = errors.New("text: the flows cross too often")

func drawSankey(l *sketch.Layout, opt Options) (*canvas, error) {
	s := l.Sankey
	if s == nil {
		return nil, ErrUnsupported
	}
	if c, err := sankeyColumns(s, opt); err == nil {
		return c, nil
	}
	return sankeyTable(s, opt)
}

// An item is a node's box in its column, or, for a flow that passes the
// column, the row it passes on.
type flowItem struct {
	node  int // -1 for a flow that passes
	flow  int
	layer int
	y     float64
	h     int
	top   int
	ins   []int // segments that end here
	outs  []int // segments that start here
}

// A segment is the part of a flow between one column and the next.
type flowSeg struct {
	flow     int
	from, to *flowItem
	rs, rt   int // the rows where it leaves and where it arrives
	lane     int // the column of its lane, -1 for a straight line
	first    bool
}

func sankeyColumns(s *types.GeoSankey, opt Options) (*canvas, error) {
	if len(s.Nodes) == 0 {
		return nil, errCrossed
	}
	layers := 0
	for _, n := range s.Nodes {
		layers = max(layers, n.Layer+1)
	}
	cols := make([][]*flowItem, layers)
	items := make([]*flowItem, len(s.Nodes))
	for i, n := range s.Nodes {
		if n.Layer < 0 {
			return nil, errCrossed
		}
		items[i] = &flowItem{node: i, y: n.Y, layer: n.Layer}
		cols[n.Layer] = append(cols[n.Layer], items[i])
	}
	var segs []*flowSeg
	for f, fl := range s.Flows {
		a, b := s.Nodes[fl.Source].Layer, s.Nodes[fl.Target].Layer
		if b <= a {
			return nil, errCrossed
		}
		prev := items[fl.Source]
		for k := a + 1; k <= b; k++ {
			next := items[fl.Target]
			if k < b {
				y := fl.SourceY + (fl.TargetY-fl.SourceY)*float64(k-a)/float64(b-a)
				next = &flowItem{node: -1, flow: f, y: y, layer: k}
				cols[k] = append(cols[k], next)
			}
			seg := &flowSeg{flow: f, from: prev, to: next, first: k == a+1}
			prev.outs = append(prev.outs, len(segs))
			next.ins = append(next.ins, len(segs))
			segs = append(segs, seg)
			prev = next
		}
	}

	// Box sizes and column widths.
	values := false
	for _, n := range s.Nodes {
		values = values || n.Value != ""
	}
	colW := make([]int, layers)
	for k := range cols {
		colW[k] = 3
		sort.SliceStable(cols[k], func(i, j int) bool { return cols[k][i].y < cols[k][j].y })
		for _, it := range cols[k] {
			if it.node < 0 {
				it.h = 1
				continue
			}
			n := s.Nodes[it.node]
			lines := 1
			if n.Value != "" {
				lines = 2
			}
			it.h = max(lines, len(it.ins), len(it.outs)) + 2
			colW[k] = max(colW[k], width(n.Label)+4, width(n.Value)+4)
		}
	}
	// The rows: each column is stacked in the layout's order, every item as near
	// to the layout's place for it as the items above it leave room for.
	tall := 0
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, col := range cols {
		h := 0
		for _, it := range col {
			h += it.h + 1
			lo, hi = math.Min(lo, it.y), math.Max(hi, it.y)
		}
		tall = max(tall, h-1)
	}
	title := 0
	for _, line := range s.Title {
		title = max(title, width(line))
	}
	y0 := 0
	if len(s.Title) > 0 {
		y0 = len(s.Title) + 1
	}
	bottom := 0
	for _, col := range cols {
		next := y0
		var last *flowItem
		for _, it := range col {
			frac := 0.5
			if hi > lo {
				frac = (it.y - lo) / (hi - lo)
			}
			want := y0 + int(math.Round(frac*float64(tall-1)-float64(it.h)/2))
			gap := 1
			if last != nil && last.node < 0 && it.node < 0 {
				gap = 0
			}
			if last != nil {
				next = last.top + last.h + gap
			}
			it.top = max(want, next)
			last = it
			bottom = max(bottom, it.top+it.h)
		}
	}
	// Ports: the rows where segments leave and arrive, in the order of the rows
	// at their other ends, so that the lines of one box do not cross.
	ports := func(it *flowItem, list []int, other func(*flowSeg) float64, set func(*flowSeg, int)) {
		sort.SliceStable(list, func(i, j int) bool { return other(segs[list[i]]) < other(segs[list[j]]) })
		if it.node < 0 {
			for _, k := range list {
				set(segs[k], it.top)
			}
			return
		}
		start := it.top + 1 + (it.h-2-len(list))/2
		for i, k := range list {
			set(segs[k], start+i)
		}
	}
	for _, col := range cols {
		for _, it := range col {
			ports(it, it.ins, func(g *flowSeg) float64 { return float64(g.rs) }, func(g *flowSeg, r int) { g.rt = r })
			ports(it, it.outs, func(g *flowSeg) float64 {
				return float64(g.to.top) + float64(g.to.h)/2 + s.Flows[g.flow].TargetY*1e-9
			}, func(g *flowSeg, r int) { g.rs = r })
		}
	}

	// The gutters between columns: the value, then a lane per segment that
	// turns, then the arrow.
	gutters := make([][]*flowSeg, max(layers-1, 0))
	for _, g := range segs {
		gutters[g.from.layer] = append(gutters[g.from.layer], g)
	}
	valueW := make([]int, len(gutters))
	lanes := make([]int, len(gutters))
	crossings := 0
	for k, gs := range gutters {
		for _, g := range gs {
			if g.first && values {
				valueW[k] = max(valueW[k], width(s.Flows[g.flow].Value))
			}
		}
		n, cross := assignLanes(gs)
		lanes[k] = n
		crossings += cross
		if crossings > maxCrossings {
			return nil, errCrossed
		}
	}
	x := make([]int, layers)
	wd := 0
	for k := range cols {
		if k > 0 {
			gw := 1 + lanes[k-1] + 2
			if valueW[k-1] > 0 {
				gw += valueW[k-1] + 1
			}
			x[k] = x[k-1] + colW[k-1] + gw
		}
		wd = x[k] + colW[k]
	}
	wd = max(wd, title)
	if opt.Cols > 0 && wd > opt.Cols {
		return nil, ErrTooWide
	}
	c, err := newCanvas(wd, max(bottom, 1))
	if err != nil {
		return nil, err
	}
	for i, line := range s.Title {
		c.text((wd-width(line))/2, i, line, roleTitle)
	}
	for k, col := range cols {
		for _, it := range col {
			if it.node < 0 {
				c.hline(x[k], x[k]+colW[k]-1, it.top, light, seriesRole(s.Flows[it.flow].Source))
				continue
			}
			n := s.Nodes[it.node]
			role := seriesRole(it.node)
			c.box(x[k], it.top, colW[k], it.h, nil, role, roleText)
			c.text(x[k]+2, it.top+1, n.Label, roleText)
			if n.Value != "" {
				c.text(x[k]+2, it.top+2, n.Value, roleNote)
			}
		}
	}
	for k, gs := range gutters {
		bx := x[k] + colW[k] - 1 // the right edge of the column
		tx := x[k+1]             // the left edge of the next one
		laneX := bx + 2
		if valueW[k] > 0 {
			laneX += valueW[k] + 1
		}
		for _, g := range gs {
			role := seriesRole(s.Flows[g.flow].Source)
			end := tx - 1
			if g.to.node < 0 {
				end = tx
			}
			if g.from.node >= 0 {
				c.stroke(bx, g.rs, right, light, role)
			}
			if g.lane < 0 {
				c.hline(bx, end, g.rs, light, role)
			} else {
				lx := laneX + g.lane
				c.hline(bx, lx, g.rs, light, role)
				c.vline(lx, g.rs, g.rt, light, role)
				c.hline(lx, end, g.rt, light, role)
			}
			if g.first && values {
				c.text(bx+2, g.rs, s.Flows[g.flow].Value, roleNote)
			}
			if g.to.node >= 0 {
				c.put(tx-1, g.rt, '▶', role)
			}
		}
	}
	return c, nil
}

// assignLanes gives each segment of a gutter that changes rows a lane, and
// counts the places where two segments cross. A segment that runs down takes
// a lane further right the higher it starts, and one that runs up the lower it
// starts: then two segments cross only where one of them spans both ends of
// the other. The order of the two groups is the one with fewer crossings.
func assignLanes(gs []*flowSeg) (lanes, crossings int) {
	var down, up []*flowSeg
	for _, g := range gs {
		switch {
		case g.rt > g.rs:
			down = append(down, g)
		case g.rt < g.rs:
			up = append(up, g)
		default:
			g.lane = -1
		}
	}
	sort.SliceStable(down, func(i, j int) bool { return down[i].rs > down[j].rs })
	sort.SliceStable(up, func(i, j int) bool { return up[i].rs < up[j].rs })
	place := func(groups ...[]*flowSeg) int {
		n := 0
		for _, group := range groups {
			for _, g := range group {
				g.lane = n
				n++
			}
		}
		return countCrossings(gs)
	}
	best := place(down, up)
	if other := place(up, down); other > best {
		place(down, up)
	} else {
		best = other
	}
	return len(down) + len(up), best
}

// countCrossings counts where a horizontal part of one segment meets the lane
// of another. Meeting at a corner counts too: the lines would join there.
func countCrossings(gs []*flowSeg) int {
	n := 0
	const straight = math.MaxInt
	for _, a := range gs {
		if a.lane < 0 {
			continue
		}
		lo, hi := min(a.rs, a.rt), max(a.rs, a.rt)
		for _, b := range gs {
			if a == b {
				continue
			}
			bl := b.lane
			if bl < 0 {
				bl = straight
				if b.rs >= lo && b.rs <= hi {
					n++
				}
				continue
			}
			if b.rs >= lo && b.rs <= hi && a.lane < bl {
				n++
			}
			if b.rt >= lo && b.rt <= hi && a.lane > bl {
				n++
			}
		}
	}
	return n
}

// sankeyTable draws the flows as a table: source, target and value, the
// flows of each source together, the sources in the order of the source text.
func sankeyTable(s *types.GeoSankey, opt Options) (*canvas, error) {
	const (
		headSource = "Source"
		headTarget = "Target"
		headValue  = "Value"
	)
	sw, tw, vw := width(headSource), width(headTarget), width(headValue)
	bySource := make([][]int, len(s.Nodes))
	var sources []int // in the order they first send a flow
	for f, fl := range s.Flows {
		if len(bySource[fl.Source]) == 0 {
			sources = append(sources, fl.Source)
		}
		bySource[fl.Source] = append(bySource[fl.Source], f)
		sw = max(sw, width(s.Nodes[fl.Source].Label))
		tw = max(tw, width(s.Nodes[fl.Target].Label))
		vw = max(vw, width(fl.Value))
	}
	// Each cell has a space either side; four rules between and around them.
	if opt.Cols > 0 && sw+tw+vw+10 > opt.Cols {
		room := opt.Cols - 10 - vw
		if room < 12 {
			return nil, ErrTooWide
		}
		// Each column keeps its longest word whole while the other keeps six
		// cells.
		sWord, tWord := 0, 0
		for _, fl := range s.Flows {
			sWord = max(sWord, longestWord(s.Nodes[fl.Source].Label))
			tWord = max(tWord, longestWord(s.Nodes[fl.Target].Label))
		}
		s2 := min(sw, max(room*sw/(sw+tw), min(sWord, room-6)), room-min(tWord, room-6, tw))
		s2 = max(s2, min(6, room/2))
		tw = min(tw, room-s2)
		sw = s2
	}
	wd := sw + tw + vw + 10
	limit := widest(s.Title)
	if opt.Cols > 0 {
		limit = min(limit, opt.Cols)
	}
	var title []string
	for _, line := range s.Title {
		title = append(title, wrap(line, max(limit, wd))...)
	}
	wd = max(wd, widest(title))
	type row struct {
		source, target []string
		value          string
		role           uint8
		first          bool
	}
	var rows []row
	height := 3 // the rules at the top, under the head and at the bottom
	if len(title) > 0 {
		height += len(title) + 1
	}
	height++ // the head
	groups := 0
	for _, i := range sources {
		flows := bySource[i]
		groups++
		for j, f := range flows {
			r := row{target: wrap(s.Nodes[s.Flows[f].Target].Label, tw), value: s.Flows[f].Value, role: seriesRole(i), first: j == 0}
			if j == 0 {
				r.source = wrap(s.Nodes[i].Label, sw)
			}
			rows = append(rows, r)
			height += max(len(r.source), len(r.target), 1)
		}
	}
	height += max(groups-1, 0)
	c, err := newCanvas(wd, height)
	if err != nil {
		return nil, err
	}
	for i, line := range title {
		c.text((wd-width(line))/2, i, line, roleTitle)
	}
	top := 0
	if len(title) > 0 {
		top = len(title) + 1
	}
	x1 := sw + 3      // the rule between source and target
	x2 := x1 + tw + 3 // the rule between target and value
	x3 := x2 + vw + 3 // the right edge
	rule := func(y int) { c.hline(0, x3, y, light, roleFrame) }
	rule(top)
	c.text(2, top+1, headSource, roleHead)
	c.text(x1+2, top+1, headTarget, roleHead)
	c.text(x3-1-width(headValue), top+1, headValue, roleHead)
	rule(top + 2)
	y := top + 3
	for i, r := range rows {
		if r.first && i > 0 {
			rule(y)
			y++
		}
		for j, line := range r.source {
			c.text(2, y+j, line, r.role)
		}
		for j, line := range r.target {
			c.text(x1+2, y+j, line, roleText)
		}
		c.text(x3-1-width(r.value), y, r.value, roleNote)
		y += max(len(r.source), len(r.target), 1)
	}
	rule(y)
	for _, x := range []int{0, x1, x2, x3} {
		c.vline(x, top, y, light, roleFrame)
	}
	return c, nil
}

func longestWord(s string) int {
	n := 0
	for _, w := range strings.Fields(s) {
		n = max(n, width(w))
	}
	return n
}
