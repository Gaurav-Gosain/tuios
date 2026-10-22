package text

import (
	"math"
	"slices"

	"tuios.dev/sketch"
)

// drawSwimlane draws a swimlane diagram as a flowchart whose lanes are frames.
// A box in cells can be longer than its node in the layout, and a frame grows
// around what it holds. So that the lanes stay one band each, of one length,
// every lane first takes the length along the flow that its longest member
// needs in cells.
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
	// The lanes of a flow that runs across are bands one above the other, all
	// at the same x.
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
		// The box drawFlowchart draws, and the room a frame keeps around it:
		// two cells each side, two rows above and one below.
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
