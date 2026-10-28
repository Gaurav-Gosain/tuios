// Ported from pele src/diagrams/treemap/layout.ts at 8aff183be57021b533f319a29341a529be934b08.

package treemap

import (
	"math"
	"slices"

	"tuios.dev/sketch/internal/js"
)

type Box struct {
	node   *TreemapNode
	parent *Box
	// series is the position among the top-level nodes of the branch this box belongs to, which picks its color.
	series int
	// value is a leaf's own value, or the sum of the leaves under a section.
	value    float64
	children []*Box
	// headed and header: small sections give their header space to their children.
	headed     bool
	header     float64
	x, y, w, h float64
}

type Insets struct {
	gap float64
	// header and side: space a section keeps for its header, and around its children on the other three sides.
	header float64
	short  float64
	side   float64
}

func newBox(node *TreemapNode, parent *Box, series int) *Box {
	value := 0.0
	if !node.section && !math.IsNaN(node.value) && !math.IsInf(node.value, 0) && node.value > 0 {
		value = node.value
	}
	return &Box{node: node, parent: parent, series: series, value: value}
}

func clamp(v, low, high float64) float64 {
	if v >= low {
		return math.Min(v, high)
	}
	return low
}

// squarify lays out boxes in the rectangle as one squarified treemap: rows are added along the
// shorter side, and a row stops growing when another box would make its boxes less square.
func squarify(boxes []*Box, total, x, y, w, h, gap float64) {
	rest := total
	from := 0
	for from < len(boxes) {
		scale := (w * h) / rest
		side := math.Min(w, h)
		sum := float64(boxes[from].value * scale)
		largest := sum
		worst := math.Max((side*side*largest)/(sum*sum), (sum*sum)/(side*side*largest))
		to := from + 1
		for to < len(boxes) {
			smallest := float64(boxes[to].value * scale)
			next := sum + smallest
			ratio := math.Max((side*side*largest)/(next*next), (next*next)/(side*side*smallest))
			if ratio > worst {
				break
			}
			worst = ratio
			sum = next
			to++
		}

		thickness := sum / side
		offset := 0.0
		for i := from; i < to; i++ {
			b := boxes[i]
			length := (b.value * scale) / thickness
			if i == to-1 {
				length = side - offset
			}
			bx, by := x+offset, y
			if w >= h {
				bx, by = x, y+offset
			}
			bw, bh := length, thickness
			if w >= h {
				bw, bh = thickness, length
			}
			// Omit the gap if it would erase the box.
			gx, gy := 0.0, 0.0
			if js.Round(bx+bw-gap/2) > js.Round(bx+gap/2) {
				gx = gap / 2
			}
			if js.Round(by+bh-gap/2) > js.Round(by+gap/2) {
				gy = gap / 2
			}
			b.x = js.Round(bx + gx)
			b.y = js.Round(by + gy)
			b.w = js.Round(bx+bw-gx) - b.x
			b.h = js.Round(by+bh-gy) - b.y
			offset += length
			rest -= b.value
		}
		if w >= h {
			x += thickness
			w -= thickness
		} else {
			y += thickness
			h -= thickness
		}
		from = to
	}
}

// layoutTreemap returns boxes with positive values, including zero-size boxes, parents before children.
func layoutTreemap(roots []*TreemapNode, width, height float64, insets Insets) []*Box {
	top := make([]*Box, len(roots))
	for index, node := range roots {
		top[index] = newBox(node, nil, index)
	}
	all := slices.Clone(top)
	for i := 0; i < len(all); i++ {
		parent := all[i]
		for _, node := range parent.node.children {
			child := newBox(node, parent, parent.series)
			parent.children = append(parent.children, child)
			all = append(all, child)
		}
	}
	for i := len(all) - 1; i >= 0; i-- {
		if b := all[i]; b.parent != nil {
			b.parent.value += b.value
		}
	}

	gap, header, short, side := insets.gap, insets.header, insets.short, insets.side
	var placed []*Box
	place := func(children []*Box, x, y, w, h float64) {
		var shown []*Box
		for _, b := range children {
			if b.value > 0 {
				shown = append(shown, b)
			}
		}
		// b.value - a.value is NaN only for two infinite sums, and then the total is not finite
		// and nothing is placed, whatever the order.
		slices.SortStableFunc(shown, func(a, b *Box) int {
			switch d := b.value - a.value; {
			case d < 0:
				return -1
			case d > 0:
				return 1
			}
			return 0
		})
		total := 0.0
		for _, b := range shown {
			total += b.value
		}
		if len(shown) == 0 || math.IsNaN(total) || math.IsInf(total, 0) {
			return
		}
		w = math.Max(0, w)
		h = math.Max(0, h)
		if w > 0 && h > 0 {
			squarify(shown, total, x-gap/2, y-gap/2, w+gap, h+gap, gap)
		}
		for _, b := range shown {
			x0 := clamp(b.x, x, x+w)
			y0 := clamp(b.y, y, y+h)
			b.w = clamp(b.x+b.w, x0, x+w) - x0
			b.h = clamp(b.y+b.h, y0, y+h) - y0
			b.x = x0
			b.y = y0
			placed = append(placed, b)
		}
	}

	place(top, 0, 0, width, height)
	for i := 0; i < len(placed); i++ {
		b := placed[i]
		if len(b.children) == 0 {
			continue
		}
		top := short
		if b.h-header-side >= 12 {
			top = header
		}
		b.headed = b.w > math.Max(24, float64(2*side)) && b.h-top-side >= 8
		b.header = 0
		if b.headed {
			b.header = top
		}
		if b.headed {
			place(b.children, b.x+side, b.y+top, b.w-float64(2*side), b.h-top-side)
		} else {
			place(b.children, b.x, b.y, b.w, b.h)
		}
	}
	return placed
}
