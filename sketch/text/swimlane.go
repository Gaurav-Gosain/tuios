package text

import (
	"math"
	"slices"

	sketch "tuios.dev/sketch/internal/engine"
)

func drawSwimlane(l *sketch.Layout, opt Options, squeeze bool) (*canvas, error) {
	var lanes []int
	for i, n := range l.Nodes {
		if n.Group && n.Parent < 0 {
			lanes = append(lanes, i)
		}
	}
	if len(lanes) == 0 {
		return drawFlowchart(l, opt, squeeze)
	}
	across, down := len(lanes) == 1, len(lanes) == 1
	for _, i := range lanes[1:] {
		if math.Abs(l.Nodes[i].X-l.Nodes[lanes[0]].X) < 0.5 {
			across = true
		} else {
			down = true
		}
	}
	sx, sy := scaleOf(l, &graphKind{})
	x0, x1, y0, y1 := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	grow := func(left, top, right, bottom float64) {
		x0, x1 = math.Min(x0, left), math.Max(x1, right)
		y0, y1 = math.Min(y0, top), math.Max(y1, bottom)
	}
	for _, i := range lanes {
		n := l.Nodes[i]
		grow(n.X-n.W/2, n.Y-n.H/2, n.X+n.W/2, n.Y+n.H/2)
	}
	for _, n := range l.Nodes {
		if n.Group || n.W <= 0 {
			continue
		}
		dw, dh := shapeRoom(n.Shape, len(n.Label), len(n.Label)+2)
		w := float64(widest(n.Label) + 4 + dw)
		w = math.Max(w/sx, n.W) / 2
		h := math.Max(float64(len(n.Label)+2+dh)/sy, n.H) / 2
		grow(n.X-w-2/sx, n.Y-h-2/sy, n.X+w+2/sx, n.Y+h+1/sy)
	}
	moved := *l
	moved.Nodes = slices.Clone(l.Nodes)
	for _, i := range lanes {
		n := &moved.Nodes[i]
		if across {
			n.X, n.W = (x0+x1)/2, x1-x0
		}
		if down {
			n.Y, n.H = (y0+y1)/2, y1-y0
		}
	}
	return drawFlowchart(&moved, opt, squeeze)
}
