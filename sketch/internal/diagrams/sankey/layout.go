// Ported from pele src/diagrams/sankey/layout.ts at 8aff183be57021b533f319a29341a529be934b08.

package sankey

import (
	"math"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

type Alignment = string

type LayoutOptions struct {
	width       float64
	height      float64
	nodeWidth   float64
	nodePadding float64
	// Height of a label line; thin nodes are kept at least this far apart where there is room.
	labelHeight float64
	alignment   Alignment
}

type PlacedNode struct {
	layer int
	// Total flow through the node, in the diagram's units.
	value  float64
	x      float64
	y      float64
	height float64
}

type PlacedLink struct {
	source    int
	target    int
	value     float64
	thickness float64
	// Vertical centers of the band where it leaves its source and where it enters its target.
	sourceY float64
	targetY float64
}

const PASSES = 6

// PULL is Math.pow(0.99, pass) for each pass, as node computes it: Go's math.Pow gives the
// double below for pass 3. TestPullAgainstNode checks the table.
var PULL = [PASSES]float64{1, 0.99, 0.9801, 0.970299, 0.96059601, 0.9509900498999999}

// sort: most nodes have a handful of bands, and the built-in sort is slow to start on short lists.
func sort(list []int, compare func(a, b int) float64) {
	if len(list) > 8 {
		js.Sort(list, compare)
		return
	}
	for i := 1; i < len(list); i++ {
		item := list[i]
		j := i - 1
		for j >= 0 && compare(list[j], item) > 0 {
			list[j+1] = list[j]
			j--
		}
		list[j+1] = item
	}
}

// or is a || b for numbers.
func or(a, b float64) float64 {
	if a != 0 && !math.IsNaN(a) {
		return a
	}
	return b
}

// layoutSankey places nodes in columns by their distance from a source, sizes them by their flow, and
// then lets each column settle next to its neighbours so that the bands cross less.
func layoutSankey(model *SankeyDb, options LayoutOptions) ([]PlacedNode, []PlacedLink) {
	n := len(model.nodes)
	links := make([]PlacedLink, len(model.links))
	for k, link := range model.links {
		value := 0.0
		if !math.IsNaN(link.value) && !math.IsInf(link.value, 0) && link.value > 0 {
			value = link.value
		}
		links[k] = PlacedLink{source: link.source.index, target: link.target.index, value: value}
	}
	outgoing := make([][]int, n)
	incoming := make([][]int, n)
	for k, link := range links {
		outgoing[link.source] = append(outgoing[link.source], k)
		incoming[link.target] = append(incoming[link.target], k)
	}

	// Longest distance from a source, in topological order; what is left over lies on a cycle.
	depth := make([]int32, n)
	pending := make([]int32, n)
	order := make([]int, 0, n)
	for i := 0; i < n; i++ {
		pending[i] = int32(len(incoming[i]))
		if pending[i] == 0 {
			order = append(order, i)
		}
	}
	for q := 0; q < len(order); q++ {
		i := order[q]
		for _, k := range outgoing[i] {
			t := links[k].target
			if depth[t] < depth[i]+1 {
				depth[t] = depth[i] + 1
			}
			pending[t]--
			if pending[t] == 0 {
				order = append(order, t)
			}
		}
	}
	if len(order) < n {
		// Every node left over has a predecessor that is left over too; walking back finds the loop.
		stuck := 0
		for pending[stuck] == 0 {
			stuck++
		}
		seen := make([]bool, n)
		for !seen[stuck] {
			seen[stuck] = true
			for _, k := range incoming[stuck] {
				if pending[links[k].source] > 0 {
					stuck = links[k].source
					break
				}
			}
		}
		types.Throw(`Sankey diagrams cannot have circular links, and "`+model.nodes[stuck].id+`" is part of one.`,
			types.CodeSemantic, types.ErrorInfo{Type: "sankey"})
	}
	layers := 1
	for i := 0; i < n; i++ {
		if int(depth[i])+1 > layers {
			layers = int(depth[i]) + 1
		}
	}
	reach := make([]int32, n)
	for q := n - 1; q >= 0; q-- {
		i := order[q]
		for _, k := range outgoing[i] {
			if reach[i] < reach[links[k].target]+1 {
				reach[i] = reach[links[k].target] + 1
			}
		}
	}

	// Flows are scaled to the largest one, so that sums of huge values stay finite.
	largest := 0.0
	for _, link := range links {
		if link.value > largest {
			largest = link.value
		}
	}
	unit := 0.0
	if largest > 0 {
		unit = 1 / largest
	}

	nodes := make([]PlacedNode, 0, n)
	flow := make([]float64, n)
	columns := make([][]int, layers)
	step := 0.0
	if layers > 1 {
		step = (options.width - options.nodeWidth) / float64(layers-1)
	}
	for i := 0; i < n; i++ {
		var layer int
		switch options.alignment {
		case "left":
			layer = int(depth[i])
		case "right":
			layer = layers - 1 - int(reach[i])
		case "center":
			layer = int(depth[i])
			if len(incoming[i]) == 0 && len(outgoing[i]) > 0 {
				layer = layers
				for _, k := range outgoing[i] {
					if int(depth[links[k].target])-1 < layer {
						layer = int(depth[links[k].target]) - 1
					}
				}
			}
		default:
			if len(outgoing[i]) > 0 {
				layer = int(depth[i])
			} else {
				layer = layers - 1
			}
		}
		into, out, value, valueOut := 0.0, 0.0, 0.0, 0.0
		for _, k := range incoming[i] {
			into += float64(links[k].value * unit)
			value += links[k].value
		}
		for _, k := range outgoing[i] {
			out += float64(links[k].value * unit)
			valueOut += links[k].value
		}
		flow[i] = math.Max(into, out)
		nodes = append(nodes, PlacedNode{layer: layer, value: math.Max(value, valueOut), x: float64(layer) * step})
		columns[layer] = append(columns[layer], i)
	}

	tallest := 1
	for _, column := range columns {
		if len(column) > tallest {
			tallest = len(column)
		}
	}
	gap := options.nodePadding
	if tallest > 1 {
		gap = math.Min(options.nodePadding, options.height/float64(tallest-1))
	}
	scale := math.Inf(1)
	for _, column := range columns {
		sum := 0.0
		for _, i := range column {
			sum += flow[i]
		}
		if sum > 0 {
			scale = math.Min(scale, (options.height-float64(float64(len(column)-1)*gap))/sum)
		}
	}
	if math.IsNaN(scale) || math.IsInf(scale, 0) || scale < 0 {
		scale = 0
	}

	// A thin node still needs room for its label. Where every column can afford it, each node
	// claims at least a label's height, and the flows are scaled down to make that fit.
	claim := math.Max(0, options.labelHeight-gap)
	fits := func(s float64) bool {
		for _, column := range columns {
			extent := float64(len(column)-1) * gap
			for _, i := range column {
				extent += math.Max(flow[i]*s, claim)
			}
			if extent > options.height+1e-6 {
				return false
			}
		}
		return true
	}
	if claim > 0 && !fits(scale) {
		if fits(0) {
			low := 0.0
			for k := 0; k < 40; k++ {
				mid := (low + scale) / 2
				if fits(mid) {
					low = mid
				} else {
					scale = mid
				}
			}
			scale = low
		} else {
			claim = 0
		}
	}

	// The room a node keeps clear above and below itself.
	margin := make([]float64, n)
	for k := range links {
		links[k].thickness = float64(links[k].value * unit * scale)
	}
	for _, column := range columns {
		y := 0.0
		for _, i := range column {
			nodes[i].height = float64(flow[i] * scale)
			margin[i] = math.Max(0, claim-nodes[i].height) / 2
			nodes[i].y = y + margin[i]
			y += nodes[i].height + float64(2*margin[i]) + gap
		}
		// Spare room is shared out between the nodes of a column.
		spare := (options.height - y + gap) / float64(len(column)+1)
		for k, i := range column {
			nodes[i].y += float64(spare * float64(k+1))
		}
	}

	center := func(i int) float64 { return nodes[i].y + nodes[i].height/2 }

	// Bands leave and enter a node in the order of where they go, so they do not cross at it.
	// These are the offsets of each band's middle from the top of the node at either end.
	sourceOffset := make([]float64, len(links))
	targetOffset := make([]float64, len(links))
	byTarget := func(a, b int) float64 { return or(center(links[a].target)-center(links[b].target), float64(a-b)) }
	bySource := func(a, b int) float64 { return or(center(links[a].source)-center(links[b].source), float64(a-b)) }
	byCenter := func(a, b int) float64 { return or(center(a)-center(b), float64(a-b)) }
	stackOut := func(i int) {
		y := 0.0
		sort(outgoing[i], byTarget)
		for _, k := range outgoing[i] {
			sourceOffset[k] = y + links[k].thickness/2
			y += links[k].thickness
		}
	}
	stackIn := func(i int) {
		y := 0.0
		sort(incoming[i], bySource)
		for _, k := range incoming[i] {
			targetOffset[k] = y + links[k].thickness/2
			y += links[k].thickness
		}
	}
	stackAll := func() {
		for i := 0; i < n; i++ {
			stackOut(i)
			stackIn(i)
		}
	}

	// settle moves each node of a column towards where its bands would run level, then restores the
	// order and the gaps. `forward` looks at the bands coming in, otherwise at those going out.
	settle := func(column []int, pull float64, forward bool) {
		for _, i := range column {
			list := outgoing[i]
			if forward {
				list = incoming[i]
			}
			if len(list) == 0 {
				continue
			}
			if forward {
				stackIn(i)
			} else {
				stackOut(i)
			}
			weighted, weight, plain := 0.0, 0.0, 0.0
			for _, k := range list {
				var level float64
				var other int
				if forward {
					level = nodes[links[k].source].y + sourceOffset[k] - targetOffset[k]
					other = links[k].source
				} else {
					level = nodes[links[k].target].y + targetOffset[k] - sourceOffset[k]
					other = links[k].target
				}
				weighted += float64(level * links[k].thickness)
				weight += links[k].thickness
				plain += center(other) - nodes[i].height/2
			}
			wanted := plain / float64(len(list))
			if weight > 0 {
				wanted = weighted / weight
			}
			nodes[i].y += float64((wanted - nodes[i].y) * pull)
		}
		sort(column, byCenter)
		y := 0.0
		for _, i := range column {
			if nodes[i].y-margin[i] < y {
				nodes[i].y = y + margin[i]
			}
			y = nodes[i].y + nodes[i].height + margin[i] + gap
		}
		y = options.height
		for k := len(column) - 1; k >= 0; k-- {
			i := column[k]
			if nodes[i].y+nodes[i].height+margin[i] > y {
				nodes[i].y = y - nodes[i].height - margin[i]
			}
			y = nodes[i].y - margin[i] - gap
		}
	}

	stackAll()
	for pass := 0; pass < PASSES; pass++ {
		pull := PULL[pass]
		for l := layers - 2; l >= 0; l-- {
			settle(columns[l], pull, false)
		}
		stackAll()
		for l := 1; l < layers; l++ {
			settle(columns[l], pull, true)
		}
		stackAll()
	}
	for k := range links {
		links[k].sourceY = nodes[links[k].source].y + sourceOffset[k]
		links[k].targetY = nodes[links[k].target].y + targetOffset[k]
	}

	return nodes, links
}
