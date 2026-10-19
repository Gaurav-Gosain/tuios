// Ported from pele src/layout/layered.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

// Package layout holds pele's own graph layouts: layered (Sugiyama style),
// compound (clusters on top of layered) and lanes.
package layout

import (
	"math"
	"slices"

	"tuios.dev/sketch/internal/js"
)

// Layered (Sugiyama-style) layout of one flat graph. Ranks run down the y axis; callers
// rotate the result for other directions.
//
// What the layout of edges aims for, most important first. Where two aims pull apart, the one
// higher up decides, and each names where it is carried out.
//
// 1. Lines can be told apart. No two touch or run along each other, and none runs through a
//    node or a label. Ends on one side of a node have places of their own (`spread`), and
//    curves that would pass within a few pixels turn at levels of their own (`assignTracks`).
// 2. Lines cross as little as the order of the nodes allows (`reduceCrossings`, and the order
//    of the levels in `assignTracks`).
// 3. An edge takes the shortest and simplest way: straight if it can, else one curve, else a
//    curve with a level run. One that passes several ranks runs in line with one of its ends
//    and turns once, at the other (`alignLabels`, `straighten`). Ends slide along their sides,
//    and nodes move a little, where that makes an edge straight (`settle`, `snap`).
// 4. An edge meets the middle of a side. An end with a side to itself stays there, and ends
//    that share a side sit around the middle in the order of where they lead. An end moves off
//    the middle only for the aims above (`settle`).
// 5. The drawing is balanced, and what is alike is drawn alike. A label sits midway along the
//    line that shows, the curves either side of it have much the same room to turn in, and a gap
//    whose curves mostly cross has them all as the same plain curve rather than each on a
//    level of its own (`headRoom` in `layered`, the crossed gaps in `assignTracks`).
// 6. An edge leaves and reaches a node square to it, and an arrowhead points straight at the
//    node (`routePath` in svg/edges.ts, which draws the routes).
// 7. Nodes keep the order they were written in, where nothing above says otherwise.
// 8. The drawing stays compact. A gap between ranks grows only to hold the levels in it, or
//    to let curves that cross do so at a clear angle.

type Kind uint8

const (
	KindNode Kind = iota
	KindDummy
	KindLabel
	KindStartPort
	KindEndPort
)

type LNode struct {
	W    float64
	H    float64
	Kind Kind
	Pin  float64
	// How much of the side that edges meet may be shared out between them; 0 keeps them all at its middle.
	Span float64
	X    float64
	Y    float64
	Rank int
}

type LEdge struct {
	Tail   int
	Head   int
	Minlen int
	LabelW float64
	LabelH float64
	TailDx float64
	HeadDx float64
	// An end that is held where its offset puts it, and takes no part in sharing out a node's side.
	TailFixed bool
	HeadFixed bool
	Reversed  bool
	Points    []float64
	LabelX    float64
	LabelY    float64
}

type LayeredOptions struct {
	NodeSep float64
	EdgeSep float64
	RankSep float64
	PortSep float64
	// Route edges at right angles, each on a track of its own across the gap between two ranks.
	Tracks bool
	// Draw every edge straight from point to point: none is given a level to turn onto.
	Straight bool
	// The caller sets the ends of the edges on one side of a node apart itself, so two edges
	// between the same two nodes need no bend to keep them from being drawn as one.
	Apart bool
	// The room that the marker at the head of an edge takes. A rank of labels whose edges all
	// head the same way sits half of this nearer their tails, so that each label is midway along
	// the line that shows, and the curves either side of it have the same room to turn in.
	HeadRoom float64
}

type LayeredResult struct {
	Width  float64
	Height float64
}

func Lnode(w, h float64, kind Kind) *LNode {
	return &LNode{W: w, H: h, Kind: kind}
}

func Ledge(tail, head, minlen int, labelW, labelH float64) *LEdge {
	return &LEdge{Tail: tail, Head: head, Minlen: minlen, LabelW: labelW, LabelH: labelH, Points: []float64{}}
}

const MAX_SWEEPS = 24

// An edge gets a bend point in every rank it skips. Past this many, the longest edges are
// drawn as one direct curve instead, which keeps huge graphs from exhausting time and memory.
const MAX_DUMMIES = 20000

// The room between two edges that meet one side of a node, and the least that keeps their arrowheads apart.
const (
	PORT_PITCH = 16
	MIN_PITCH  = 8
)

// Where edges are not given levels, a gap between ranks grows to keep the edges that cross it at
// least this steep, up to this many times its usual height. Shallower than that, the edges of a
// wide rank are hard to tell apart.
const (
	MIN_SLOPE = 0.125
	MAX_GAP   = 3
)

// Tracks: the room kept clear between a rank and the nearest track, the room between two tracks
// when the gap has to grow to hold them, and the most they spread to when it has room to spare.
const (
	TRACK_MARGIN = 18
	TRACK_PITCH  = 10
	TRACK_SPREAD = 14
)

// A curve is one plain S unless it runs more than this many times as far across as the gap is
// high, and so does another that it would run along. Then each turns onto a level of its own
// to run across on. The levels of a gap keep this far from its ranks, to leave room to turn
// in, and from each other.
const (
	LEVEL_WIDE   = 2
	LEVEL_MARGIN = 28
	LEVEL_PITCH  = 12
)

// Two plain curves that would pass too near each other turn at levels of their own as well:
// nearer than LEVEL_CLEAR where one of them runs far across, since it stays near the other for
// a long way, and otherwise nearer than LEVEL_TOUCH. LEVEL_LEAD is the room a plain curve is
// taken to keep straight at each end of its gap.
const (
	LEVEL_LEAD  = 12
	LEVEL_CLEAR = 8
	LEVEL_TOUCH = 4
)

// Levels tell apart curves that run alongside each other. Where the curves of a gap cross each
// other more often than that, runs along levels only make the crossings long and shallow, so
// each curve is one plain S instead, and the gap is made this many times as tall as its widest
// curve runs across (and no taller than MAX_GAP allows), for them to cross at a clear angle.
const CROSSED_SLOPE = 0.5

// How far out of line two points may be and still count as in line.
const IN_LINE = 0.5

// A node, with whatever it is already in line with, moves this far at most to bring one more of
// its edges into line.
const SNAP = 12

// An end that cannot be brought into line leans towards where its edge goes, but keeps this far
// from the ends of the part of its side that edges spread along. On a narrow node that leaves
// it in the middle.
const LEAN_CLEAR = 40

// Past this many edges across one gap, tracks are given out in a plain order: weighing every
// pair of edges against each other would take too long.
const MAX_WEIGHED = 400

type End = uint8

const (
	EndHeld End = iota
	EndLeaves
	EndArrives
)

func isDummy(kind Kind) bool {
	return kind == KindDummy || kind == KindLabel
}

var (
	inf    = math.Inf(1)
	negInf = math.Inf(-1)
)

// sign turns the number a JavaScript comparator returns into an int.
func sign(x float64) int {
	switch {
	case x < 0:
		return -1
	case x > 0:
		return 1
	}
	return 0
}

// sortBy is a stable sort with a JavaScript comparator, as Array.prototype.sort is.
func sortBy(s []int, cmp func(a, b int) float64) {
	slices.SortStableFunc(s, func(a, b int) int { return sign(cmp(a, b)) })
}

// or is `a || b` for numbers.
func or(a, b float64) float64 { return js.Or(a, b) }

func sortRangeInt(list []int, from, to int, key []int) {
	for i := from + 1; i < to; i++ {
		item := list[i]
		k := key[item]
		j := i - 1
		for j >= from && (key[list[j]] > k || (key[list[j]] == k && list[j] > item)) {
			list[j+1] = list[j]
			j--
		}
		list[j+1] = item
	}
}

func fillInt(n, v int) []int {
	s := make([]int, n)
	if v != 0 {
		for i := range s {
			s[i] = v
		}
	}
	return s
}

func Layered(nodes []*LNode, edges []*LEdge, opt LayeredOptions) LayeredResult {
	real := len(nodes)
	if real == 0 {
		return LayeredResult{}
	}
	m := len(edges)

	// Every edge gets a middle rank of its own when labels or parallel edges need the room.
	step := 1
	if m > 0 {
		seen := map[int]bool{}
		for _, e := range edges {
			a := min(e.Tail, e.Head)
			b := max(e.Tail, e.Head)
			key := a*real + b
			if e.LabelW > 0 || seen[key] {
				step = 2
				break
			}
			seen[key] = true
		}
	}

	Orient(nodes, edges)
	Rank(nodes, edges, step)

	span := make([]int, m)
	dummies := 0
	for ei := 0; ei < m; ei++ {
		e := edges[ei]
		span[ei] = absInt(nodes[e.Head].Rank - nodes[e.Tail].Rank)
		if span[ei] > 1 {
			dummies += span[ei] - 1
		}
	}
	direct := make([]uint8, m)
	if dummies > MAX_DUMMIES {
		var longest []int
		for ei := 0; ei < m; ei++ {
			if span[ei] > 1 {
				longest = append(longest, ei)
			}
		}
		sortBy(longest, func(a, b int) float64 { return or(float64(span[b]-span[a]), float64(a-b)) })
		for _, ei := range longest {
			if dummies <= MAX_DUMMIES {
				break
			}
			direct[ei] = 1
			dummies -= span[ei] - 1
		}
	}

	n := real + dummies
	W := make([]float64, n)
	H := make([]float64, n)
	KIND := make([]Kind, n)
	RANK := make([]int, n)
	maxRank := 0
	for i := 0; i < real; i++ {
		node := nodes[i]
		W[i] = node.W
		H[i] = node.H
		KIND[i] = node.Kind
		RANK[i] = node.Rank
		if node.Rank > maxRank {
			maxRank = node.Rank
		}
	}

	// The graph that gets ordered and positioned: every edge cut into rank-to-rank segments.
	segments := 0
	for ei := 0; ei < m; ei++ {
		if direct[ei] == 0 {
			segments += span[ei]
		}
	}
	EF := make([]int, segments)
	ET := make([]int, segments)
	EFD := make([]float64, segments)
	ETD := make([]float64, segments)
	firstDummy := fillInt(m, -1)
	// Which way the edges with a label in each rank head: 1 down, 2 up, 3 both.
	heads := make([]uint8, maxRank+1)
	labelNode := fillInt(m, -1)
	spans := false
	for i := 0; i < real; i++ {
		if nodes[i].Span > 0 && nodes[i].Kind == KindNode {
			spans = true
		}
	}
	// What each segment's end is to the node it meets, for the ends that may move along the node's side.
	var EFK, ETK []End
	if spans {
		EFK = make([]End, segments)
		ETK = make([]End, segments)
	}
	firstSeg := fillInt(m, -1)
	lastSeg := fillInt(m, -1)
	offsets := false
	{
		next := real
		ce := 0
		for ei := 0; ei < m; ei++ {
			if direct[ei] != 0 {
				continue
			}
			e := edges[ei]
			a, b := e.Tail, e.Head
			aDx, bDx := e.TailDx, e.HeadDx
			if e.Reversed {
				a, b = e.Head, e.Tail
				aDx, bDx = e.HeadDx, e.TailDx
			}
			if aDx != 0 || bDx != 0 {
				offsets = true
			}
			ra := RANK[a]
			rb := RANK[b]
			labelRank := -1
			if e.LabelW > 0 {
				// The label goes halfway along. An edge that passes ranks of nodes on its way has its
				// label in one of those, beside the nodes, where it takes no height of its own: the one
				// halfway, or of the two either side of halfway, the one nearer the node the edge starts
				// from, which is the lower when the edge runs against the flow. An edge between two
				// neighbouring ranks has its label in the rank between them.
				labelRank = ra + ((rb - ra) >> 1)
				adj := -1
				if e.Reversed {
					adj = 1
				}
				if step == 2 && rb-ra >= 4 {
					if labelRank&1 == 1 {
						labelRank += adj
					}
				} else if step == 2 && labelRank&1 == 0 {
					labelRank += adj
				}
			}
			prev := a
			if rb-ra > 1 {
				firstDummy[ei] = next
			}
			firstSeg[ei] = ce
			if EFK != nil {
				fixed := e.TailFixed
				kind := EndLeaves
				if e.Reversed {
					fixed = e.HeadFixed
					kind = EndArrives
				}
				if !fixed {
					EFK[ce] = kind
				}
			}
			for r := ra + 1; r < rb; r++ {
				d := next
				next++
				RANK[d] = r
				if r == labelRank {
					W[d] = e.LabelW
					H[d] = e.LabelH
					KIND[d] = KindLabel
					labelNode[ei] = d
					if e.Reversed {
						heads[r] |= 2
					} else {
						heads[r] |= 1
					}
				} else {
					KIND[d] = KindDummy
				}
				EF[ce] = prev
				ET[ce] = d
				if prev == a {
					EFD[ce] = aDx
				}
				ce++
				prev = d
			}
			EF[ce] = prev
			ET[ce] = b
			if prev == a {
				EFD[ce] = aDx
			}
			ETD[ce] = bDx
			if ETK != nil {
				fixed := e.HeadFixed
				kind := EndArrives
				if e.Reversed {
					fixed = e.TailFixed
					kind = EndLeaves
				}
				if !fixed {
					ETK[ce] = kind
				}
			}
			lastSeg[ei] = ce
			ce++
		}
	}

	succStart := make([]int, n+1)
	predStart := make([]int, n+1)
	for e := 0; e < segments; e++ {
		succStart[EF[e]+1]++
		predStart[ET[e]+1]++
	}
	for i := 0; i < n; i++ {
		succStart[i+1] += succStart[i]
		predStart[i+1] += predStart[i]
	}
	succE := make([]int, segments)
	predE := make([]int, segments)
	{
		sf := slices.Clone(succStart[:n])
		pf := slices.Clone(predStart[:n])
		for e := 0; e < segments; e++ {
			succE[sf[EF[e]]] = e
			sf[EF[e]]++
			predE[pf[ET[e]]] = e
			pf[ET[e]]++
		}
	}

	layerStart := make([]int, maxRank+2)
	for i := 0; i < n; i++ {
		layerStart[RANK[i]+1]++
	}
	for r := 0; r <= maxRank; r++ {
		layerStart[r+1] += layerStart[r]
	}
	order := make([]int, n)
	pos := make([]int, n)

	// Initial order: depth-first from the top so that subtrees start out contiguous.
	{
		byRank := make([]int, n)
		fill := slices.Clone(layerStart[:maxRank+1])
		for i := 0; i < n; i++ {
			byRank[fill[RANK[i]]] = i
			fill[RANK[i]]++
		}
		copy(fill, layerStart[:maxRank+1])
		visited := make([]uint8, n)
		var stack []int
		for k := 0; k < n; k++ {
			if visited[byRank[k]] != 0 {
				continue
			}
			stack = append(stack, byRank[k])
			for len(stack) > 0 {
				v := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if visited[v] != 0 {
					continue
				}
				visited[v] = 1
				order[fill[RANK[v]]] = v
				fill[RANK[v]]++
				for j := succStart[v+1] - 1; j >= succStart[v]; j-- {
					w := ET[succE[j]]
					if visited[w] == 0 {
						stack = append(stack, w)
					}
				}
			}
		}
	}

	pinned := false
	for i := 0; i < real; i++ {
		if nodes[i].Pin != 0 {
			pinned = true
		}
	}
	if pinned {
		for r := 0; r <= maxRank; r++ {
			layer := slices.Clone(order[layerStart[r]:layerStart[r+1]])
			index := map[int]int{}
			for i, v := range layer {
				index[v] = i
			}
			pinOf := func(v int) float64 {
				if v < real {
					return nodes[v].Pin
				}
				return 0
			}
			sortBy(layer, func(a, b int) float64 { return or(pinOf(a)-pinOf(b), float64(index[a]-index[b])) })
			copy(order[layerStart[r]:], layer)
		}
	}
	setPositions := func() {
		for r := 0; r <= maxRank; r++ {
			s := layerStart[r]
			for i, e := s, layerStart[r+1]; i < e; i++ {
				pos[order[i]] = i - s
			}
		}
	}
	setPositions()

	g := &Graph{n, real, maxRank, W, KIND, RANK, EF, ET, EFD, ETD, succStart, succE, predStart, predE, layerStart, order, pos, offsets}

	if segments > 0 && maxRank > 0 {
		reduceCrossings(g, nodes)
		setPositions()
	}

	if EFK != nil && ETK != nil {
		spread(g, nodes, EFK, ETK)
	}

	xs := position(g, opt)

	empty := emptyRanks(g)
	if EFK != nil && ETK != nil {
		free := freeEnds(g, EFK, ETK)
		alignLabels(g, nodes, xs, free, opt, empty)
		settle(g, nodes, xs, EFK, ETK, free, empty)
		snap(g, xs, empty, opt)
		for ei := 0; ei < m; ei++ {
			if direct[ei] != 0 {
				continue
			}
			e := edges[ei]
			aDx := EFD[firstSeg[ei]]
			bDx := ETD[lastSeg[ei]]
			if e.Reversed {
				e.TailDx, e.HeadDx = bDx, aDx
			} else {
				e.TailDx, e.HeadDx = aDx, bDx
			}
		}
	}

	// A rank that holds nothing but the bends of edges is only there because some other edge has a
	// label or a twin. Its bends move onto the straight line between the ends of their edges, and
	// one that gets there is left out of the route, so that the edge is drawn as a single curve.
	onLine := make([]uint8, n)
	if empty != nil && !opt.Tracks {
		sep := opt.EdgeSep
		if opt.Apart {
			sep = 0
		}
		straighten(g, xs, empty, firstDummy, span, sep, onLine)
	}
	if !opt.Tracks {
		pastLabels(g, xs, firstDummy, span, opt.EdgeSep, onLine)
	}
	// An edge is cut where it passes a rank that holds something, and each piece crosses one gap.
	// With `tracks`, every piece that does not run straight gets a track to run across on at
	// right angles. Otherwise, where edges spread along their nodes, a piece gets one only if it
	// needs a level to turn onto. `pieceStart` gives every edge's run of pieces.
	pieceStart := make([]int, m+1)
	var pieces *Pieces
	if opt.Tracks || (EFK != nil && !opt.Straight) {
		pieces = &Pieces{}
	}
	trackCount := make([]int, maxRank+1)
	if pieces != nil {
		for ei := 0; ei < m; ei++ {
			pieceStart[ei] = len(pieces.from)
			if direct[ei] != 0 {
				continue
			}
			d0 := firstDummy[ei]
			end := d0
			if d0 >= 0 {
				end = d0 + span[ei] - 1
			}
			// An edge that keeps a bend to stay clear of its twin is drawn through the bend, as it is.
			twin := false
			for d := d0; d < end; d++ {
				// So is one that has a bend left out in a rank of labels: its curve crosses that rank.
				bare := empty != nil && empty[RANK[d]] == 1
				if !opt.Tracks && bare != (onLine[d] == 1) {
					twin = true
				}
			}
			if twin {
				continue
			}
			x := xs[EF[firstSeg[ei]]] + EFD[firstSeg[ei]]
			rank := RANK[EF[firstSeg[ei]]]
			for d := d0; d < end; d++ {
				if empty != nil && empty[RANK[d]] != 0 {
					continue
				}
				pieces.from = append(pieces.from, x)
				pieces.to = append(pieces.to, xs[d])
				pieces.rank = append(pieces.rank, rank)
				pieces.lower = append(pieces.lower, d)
				x = xs[d]
				rank = RANK[d]
			}
			pieces.from = append(pieces.from, x)
			pieces.to = append(pieces.to, xs[ET[lastSeg[ei]]]+ETD[lastSeg[ei]])
			pieces.rank = append(pieces.rank, rank)
			pieces.lower = append(pieces.lower, -1)
		}
		pieceStart[m] = len(pieces.from)
	}

	// Ranks that hold only border ports or their pass-through dummies need little room of their own.
	portOnly := func(r int, kind Kind) bool {
		found := false
		for i := layerStart[r]; i < layerStart[r+1]; i++ {
			k := KIND[order[i]]
			if k == kind {
				found = true
			} else if k != KindDummy {
				return false
			}
		}
		return found
	}
	startRanks := 0
	if portOnly(0, KindStartPort) {
		startRanks = step
	}
	endRanks := 0
	if portOnly(maxRank, KindEndPort) {
		endRanks = step
	}
	// The longest way across that any edge travels in the gap below each rank.
	run := make([]float64, maxRank+1)
	for e := 0; e < segments; e++ {
		across := math.Abs(xs[EF[e]] + EFD[e] - xs[ET[e]] - ETD[e])
		if across > run[RANK[EF[e]]] {
			run[RANK[EF[e]]] = across
		}
	}
	bandTop := make([]float64, maxRank+1)
	bandBottom := make([]float64, maxRank+1)
	usual := func(r int) float64 {
		sep := opt.RankSep
		if r < startRanks || r >= maxRank-endRanks {
			sep = opt.PortSep
		}
		return sep / float64(step)
	}
	// The room under each rank. Edges that are not cut into pieces need more where they run far across.
	gapUnder := make([]float64, maxRank+1)
	for r := 0; r < maxRank; r++ {
		if pieces != nil {
			gapUnder[r] = usual(r)
		} else {
			gapUnder[r] = math.Max(usual(r), math.Min((MAX_GAP*opt.RankSep)/float64(step), run[r]*MIN_SLOPE))
		}
	}
	// A rank between two ranks of nodes, which holds labels and bends only.
	between := make([]uint8, maxRank+1)
	for i := range between {
		between[i] = 1
	}
	for i := 0; i < real; i++ {
		between[RANK[i]] = 0
	}
	for r := 1; r < maxRank; r++ {
		if opt.Tracks || between[r] == 0 || (heads[r] != 1 && heads[r] != 2) {
			continue
		}
		under := r
		if heads[r] == 1 {
			under = r - 1
		}
		shift := math.Min(opt.HeadRoom/2, gapUnder[under]/2)
		if heads[r] == 1 {
			gapUnder[r-1] += -shift
			gapUnder[r] += shift
		} else {
			gapUnder[r-1] += shift
			gapUnder[r] += -shift
		}
	}
	// The part of its rank that an edge runs straight through at a bend or label. A bare bend
	// beside labels keeps to the middle half, which leaves the curves either side of it more room
	// and still keeps them clear of the labels' corners.
	inset := func(d int) float64 {
		if KIND[d] == KindDummy && between[RANK[d]] != 0 && !opt.Tracks {
			return (bandBottom[RANK[d]] - bandTop[RANK[d]]) / 4
		}
		return 0
	}
	// The rank that the pieces leaving each rank arrive at, past any that hold only bends, and
	// the room between the two, which grows to hold the gap's tracks or levels: these keep
	// `margin` from the ranks, and between `leastPitch` and `mostPitch` from each other.
	margin, leastPitch, mostPitch := float64(LEVEL_MARGIN), float64(LEVEL_PITCH), float64(LEVEL_PITCH)
	if opt.Tracks {
		margin, leastPitch, mostPitch = TRACK_MARGIN, TRACK_PITCH, TRACK_SPREAD
	}
	below := make([]int, maxRank+1)
	if pieces != nil {
		room := make([]float64, maxRank+1)
		for r := 0; r < maxRank; r++ {
			if empty != nil && empty[r] != 0 {
				continue
			}
			next := r + 1
			room[r] = gapUnder[r]
			for next < maxRank && empty != nil && empty[next] != 0 {
				room[r] += gapUnder[next]
				next++
			}
			below[r] = next
		}
		for k := 0; k < len(pieces.from); k++ {
			w := 0
			if math.Abs(pieces.from[k]-pieces.to[k]) > LEVEL_WIDE*room[pieces.rank[k]] {
				w = 1
			}
			pieces.wide = append(pieces.wide, w)
		}
		tall := make([]float64, maxRank+1)
		assignTracks(pieces, trackCount, !opt.Tracks, room, tall)
		for r := 0; r < maxRank; r++ {
			if trackCount[r] > 0 {
				gapUnder[r] += math.Max(0, 2*margin+float64(trackCount[r]-1)*leastPitch-room[r])
			} else if tall[r] > 0 {
				gapUnder[r] += math.Max(0, math.Min(MAX_GAP*opt.RankSep, tall[r])-room[r])
			}
		}
	}
	y := 0.0
	for r := 0; r <= maxRank; r++ {
		h := 0.0
		for i := layerStart[r]; i < layerStart[r+1]; i++ {
			if H[order[i]] > h {
				h = H[order[i]]
			}
		}
		bandTop[r] = y
		bandBottom[r] = y + h
		y += h + gapUnder[r]
	}
	height := y
	// Where a piece runs across its gap, or would on another track of its group: its tracks sit
	// in the middle of the gap, the first at the top.
	trackY := func(piece int, onTrack int) float64 {
		r := pieces.rank[piece]
		top := bandBottom[r]
		bottom := bandTop[below[r]]
		count := pieces.levels[piece]
		if opt.Tracks {
			count = trackCount[r]
		}
		pitch := 0.0
		if count > 1 {
			pitch = math.Min(mostPitch, (bottom-top-2*margin)/float64(count-1))
		}
		return (top+bottom)/2 + (float64(onTrack)-float64(count-1)/2)*pitch
	}

	minX := inf
	maxX := negInf
	for i := 0; i < n; i++ {
		half := W[i] / 2
		if xs[i]-half < minX {
			minX = xs[i] - half
		}
		if xs[i]+half > maxX {
			maxX = xs[i] + half
		}
	}
	// Where edges spread along their nodes, a rank whose nodes only send edges on stands on one
	// line, and a rank whose nodes only receive them hangs from one, so that the edges of nodes of
	// different heights set out from, or arrive at, the same level. In any other rank the nodes
	// are centered.
	arrives := make([]uint8, maxRank+1)
	leaves := make([]uint8, maxRank+1)
	for i := 0; i < real; i++ {
		if KIND[i] != KindNode || EFK == nil {
			continue
		}
		if predStart[i+1] > predStart[i] {
			arrives[RANK[i]] = 1
		}
		if succStart[i+1] > succStart[i] {
			leaves[RANK[i]] = 1
		}
	}
	for i := 0; i < real; i++ {
		node := nodes[i]
		r := node.Rank
		node.X = xs[i] - minX
		if KIND[i] == KindNode && leaves[r] != 0 && arrives[r] == 0 {
			node.Y = bandBottom[r] - node.H/2
		} else if KIND[i] == KindNode && arrives[r] != 0 && leaves[r] == 0 {
			node.Y = bandTop[r] + node.H/2
		} else {
			node.Y = (bandTop[r] + bandBottom[r]) / 2
		}
	}

	for ei := 0; ei < m; ei++ {
		e := edges[ei]
		first, last := nodes[e.Tail], nodes[e.Head]
		firstDx, lastDx := e.TailDx, e.HeadDx
		if e.Reversed {
			first, last = nodes[e.Head], nodes[e.Tail]
			firstDx, lastDx = e.HeadDx, e.TailDx
		}
		pts := []float64{first.X + firstDx, first.Y + first.H/2}
		d0 := firstDummy[ei]
		if pieces != nil && pieceStart[ei+1] > pieceStart[ei] {
			for k := pieceStart[ei]; k < pieceStart[ei+1]; k++ {
				if pieces.track[k] >= 0 && (opt.Tracks || pieces.wide[k] == 1) {
					across := trackY(k, pieces.track[k])
					pts = append(pts, pieces.from[k]-minX, across, pieces.to[k]-minX, across)
				} else if pieces.track[k] > 0 {
					// A short piece among levels runs straight until it is past the level before its
					// own, and turns in one S from there, so that it crosses none of them.
					pts = append(pts, pieces.from[k]-minX, trackY(k, pieces.track[k]-1))
				}
				d := pieces.lower[k]
				if d < 0 {
					continue
				}
				pts = append(pts, xs[d]-minX, bandTop[RANK[d]]+inset(d))
				if bandBottom[RANK[d]] > bandTop[RANK[d]] {
					pts = append(pts, xs[d]-minX, bandBottom[RANK[d]]-inset(d))
				}
			}
		} else if d0 >= 0 {
			for d, end := d0, d0+span[ei]-1; d < end; d++ {
				if onLine[d] != 0 {
					continue
				}
				x := xs[d] - minX
				top := bandTop[RANK[d]]
				bottom := bandBottom[RANK[d]]
				pts = append(pts, x, top+inset(d))
				if bottom > top {
					pts = append(pts, x, bottom-inset(d))
				}
			}
		}
		pts = append(pts, last.X+lastDx, last.Y-last.H/2)
		if e.Reversed {
			for i, j := 0, len(pts)-2; i < j; i, j = i+2, j-2 {
				pts[i], pts[j] = pts[j], pts[i]
				pts[i+1], pts[j+1] = pts[j+1], pts[i+1]
			}
		}
		e.Points = pts
		ln := labelNode[ei]
		if ln >= 0 {
			e.LabelX = xs[ln] - minX
			e.LabelY = (bandTop[RANK[ln]] + bandBottom[RANK[ln]]) / 2
		} else if e.LabelW > 0 {
			e.LabelX = (pts[0] + pts[len(pts)-2]) / 2
			e.LabelY = (pts[1] + pts[len(pts)-1]) / 2
		}
	}

	return LayeredResult{Width: maxX - minX, Height: height}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// The pieces of edges that each cross one gap: where they leave the rank above and reach the
// one below, the rank above, the bend they end at (or -1 at the edge's last node), and the
// track they run across on (or -1 when they have none). Where only some pieces are given a
// level to turn onto, `wide` marks those that run far across, `levels` is how many tracks the
// group a piece belongs to takes, or 0 for a piece that needs none, and each piece's track
// counts from its group's first. A piece that is not `wide` curves from the level above its own.
type Pieces struct {
	from   []float64
	to     []float64
	rank   []int
	lower  []int
	track  []int
	levels []int
	wide   []int
}

type Graph struct {
	n       int
	real    int
	maxRank int
	W       []float64
	KIND    []Kind
	RANK    []int
	// Segment endpoints, and where each segment attaches on them.
	EF         []int
	ET         []int
	EFD        []float64
	ETD        []float64
	succStart  []int
	succE      []int
	predStart  []int
	predE      []int
	layerStart []int
	order      []int
	pos        []int
	offsets    bool
}

// Orient reverses the edges that close a cycle, found by depth-first search in declaration order.
func Orient(nodes []*LNode, edges []*LEdge) {
	n := len(nodes)
	m := len(edges)
	start := make([]int, n+1)
	for i := 0; i < m; i++ {
		e := edges[i]
		e.Reversed = nodes[e.Head].Kind == KindStartPort || nodes[e.Tail].Kind == KindEndPort
		if !e.Reversed {
			start[e.Tail+1]++
		}
	}
	for i := 0; i < n; i++ {
		start[i+1] += start[i]
	}
	out := make([]int, start[n])
	{
		fill := slices.Clone(start[:n])
		for i := 0; i < m; i++ {
			if !edges[i].Reversed {
				out[fill[edges[i].Tail]] = i
				fill[edges[i].Tail]++
			}
		}
	}
	state := make([]uint8, n)
	cursor := slices.Clone(start[:n])
	var stack []int
	for root := 0; root < n; root++ {
		if state[root] != 0 {
			continue
		}
		stack = append(stack, root)
		state[root] = 1
		for len(stack) > 0 {
			v := stack[len(stack)-1]
			if cursor[v] < start[v+1] {
				e := edges[out[cursor[v]]]
				cursor[v]++
				w := e.Head
				if state[w] == 1 {
					e.Reversed = true
				} else if state[w] == 0 {
					state[w] = 1
					stack = append(stack, w)
				}
			} else {
				state[v] = 2
				stack = stack[:len(stack)-1]
			}
		}
	}
}

func Rank(nodes []*LNode, edges []*LEdge, step int) {
	n := len(nodes)
	m := len(edges)
	from := make([]int, m)
	to := make([]int, m)
	length := make([]int, m)
	outStart := make([]int, n+1)
	inStart := make([]int, n+1)
	for i := 0; i < m; i++ {
		e := edges[i]
		a, b := e.Tail, e.Head
		if e.Reversed {
			a, b = e.Head, e.Tail
		}
		from[i] = a
		to[i] = b
		length[i] = max(1, e.Minlen) * step
		outStart[a+1]++
		inStart[b+1]++
	}
	for i := 0; i < n; i++ {
		outStart[i+1] += outStart[i]
		inStart[i+1] += inStart[i]
	}
	outE := make([]int, m)
	inE := make([]int, m)
	{
		of := slices.Clone(outStart[:n])
		inf := slices.Clone(inStart[:n])
		for i := 0; i < m; i++ {
			outE[of[from[i]]] = i
			of[from[i]]++
			inE[inf[to[i]]] = i
			inf[to[i]]++
		}
	}

	r := make([]int, n)
	indeg := make([]int, n)
	for i := 0; i < n; i++ {
		indeg[i] = inStart[i+1] - inStart[i]
	}
	topo := make([]int, n)
	count := 0
	for i := 0; i < n; i++ {
		if indeg[i] == 0 {
			topo[count] = i
			count++
		}
	}
	for q := 0; q < count; q++ {
		v := topo[q]
		for k := outStart[v]; k < outStart[v+1]; k++ {
			ei := outE[k]
			w := to[ei]
			if r[v]+length[ei] > r[w] {
				r[w] = r[v] + length[ei]
			}
			indeg[w]--
			if indeg[w] == 0 {
				topo[count] = w
				count++
			}
		}
	}

	// Longest-path ranking leaves loosely attached nodes far from their neighbours.
	// Move each node toward the side that has more edges.
	const none = math.MaxInt
	for pass := 0; pass < 8; pass++ {
		changed := false
		for q := count - 1; q >= 0; q-- {
			v := topo[q]
			ins := inStart[v+1] - inStart[v]
			outs := outStart[v+1] - outStart[v]
			if ins == 0 && outs == 0 {
				continue
			}
			lo := math.MinInt
			hi := none
			for k := inStart[v]; k < inStart[v+1]; k++ {
				ei := inE[k]
				lo = max(lo, r[from[ei]]+length[ei])
			}
			for k := outStart[v]; k < outStart[v+1]; k++ {
				ei := outE[k]
				hi = min(hi, r[to[ei]]-length[ei])
			}
			next := r[v]
			if outs > ins && hi != none {
				next = hi
			} else if ins > outs && lo != math.MinInt {
				next = lo
			}
			if next != r[v] {
				r[v] = next
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	lo, hi := math.MaxInt, math.MinInt
	found := false
	startPorts := false
	for i := 0; i < n; i++ {
		kind := nodes[i].Kind
		if kind == KindStartPort {
			startPorts = true
		}
		if kind == KindStartPort || kind == KindEndPort {
			continue
		}
		found = true
		if r[i] < lo {
			lo = r[i]
		}
		if r[i] > hi {
			hi = r[i]
		}
	}
	if !found {
		lo = 0
		hi = 0
	}
	for i := 0; i < n; i++ {
		if nodes[i].Kind == KindStartPort {
			r[i] = lo - step
		} else if nodes[i].Kind == KindEndPort {
			r[i] = hi + step
		}
	}
	if startPorts {
		lo -= step
	}
	for i := 0; i < n; i++ {
		nodes[i].Rank = r[i] - lo
	}
}

// The ends of edges that may move along one side of a node (0 the side its edges arrive at, 1 the
// other), in the order of the nodes at their other ends, so that no two cross. `room` receives
// the least distance between each end and the one before it, and the result is their sum.
// When there is too little room for a full pitch between each pair, the edges that leave side by
// side move closer together, since they have no arrowheads to keep apart. With less room still,
// neighbours that run the same way share a place, which keeps an arrowhead apart from an edge
// that leaves beside it.
func sideEnds(g *Graph, v int, side int, span float64, kinds []End, ids *[]int, room *[]float64) float64 {
	start, list, other := g.succStart, g.succE, g.ET
	if side == 0 {
		start, list, other = g.predStart, g.predE, g.EF
	}
	pos := g.pos
	*ids = (*ids)[:0]
	for k := start[v]; k < start[v+1]; k++ {
		if kinds[list[k]] != EndHeld {
			*ids = append(*ids, list[k])
		}
	}
	if len(*ids) < 2 {
		return 0
	}
	sortBy(*ids, func(a, b int) float64 { return or(float64(pos[other[a]]-pos[other[b]]), float64(a-b)) })
	fill := func(share func(leaves, same bool) float64) float64 {
		total := 0.0
		l := *ids
		if cap(*room) < len(l) {
			*room = make([]float64, len(l))
		}
		*room = (*room)[:len(l)]
		r := *room
		r[0] = 0
		for k := 1; k < len(l); k++ {
			same := kinds[l[k]] == kinds[l[k-1]]
			r[k] = share(same && kinds[l[k]] == EndLeaves, same)
			total += r[k]
		}
		return total
	}
	total := fill(func(bool, bool) float64 { return 1 })
	if span/total < MIN_PITCH {
		total = fill(func(leaves, _ bool) float64 {
			if leaves {
				return 0.5
			}
			return 1
		})
	}
	if span/total < MIN_PITCH {
		total = fill(func(_ bool, same bool) float64 {
			if same {
				return 0
			}
			return 1
		})
	}
	if total == 0 {
		return 0
	}
	pitch := math.Min(PORT_PITCH, span/total)
	r := *room
	for k := 1; k < len(*ids); k++ {
		r[k] *= pitch
	}
	return total * pitch
}

// Gives the edges that meet one side of a node places of their own along it, around its middle.
func spread(g *Graph, nodes []*LNode, EFK, ETK []End) {
	W, EFD, ETD := g.W, g.EFD, g.ETD
	var ids []int
	var room []float64
	for v := 0; v < g.real; v++ {
		span := math.Min(nodes[v].Span, W[v])
		if span <= 0 || nodes[v].Kind != KindNode {
			continue
		}
		for side := 0; side < 2; side++ {
			kinds := EFK
			if side == 0 {
				kinds = ETK
			}
			total := sideEnds(g, v, side, span, kinds, &ids, &room)
			if total == 0 {
				continue
			}
			dx := EFD
			if side == 0 {
				dx = ETD
			}
			at := -total / 2
			for k := 0; k < len(ids); k++ {
				at += room[k]
				dx[ids[k]] = at
			}
		}
	}
}

// The segment of an edge that reaches the next point held in place, on the way up from segment
// `e` (side 0) or down: past the bends in ranks that hold nothing else.
func follow(g *Graph, empty []uint8, e int, side int) int {
	if empty == nil {
		return e
	}
	for hops := 0; hops < 100000; hops++ {
		u := g.ET[e]
		if side == 0 {
			u = g.EF[e]
		}
		if g.KIND[u] != KindDummy || empty[g.RANK[u]] == 0 {
			break
		}
		if side == 0 {
			e = g.predE[g.predStart[u]]
		} else {
			e = g.succE[g.succStart[u]]
		}
	}
	return e
}

// How many ends that may move along it each node has on the side its edges arrive at, and on
// the other. An end that has a side to itself keeps to the middle; ends that share one cannot
// all be there, so those may move.
func freeEnds(g *Graph, EFK, ETK []End) [2][]int {
	free := [2][]int{make([]int, g.n), make([]int, g.n)}
	for e := 0; e < len(g.EF); e++ {
		if ETK[e] != EndHeld {
			free[0][g.ET[e]]++
		}
		if EFK[e] != EndHeld {
			free[1][g.EF[e]]++
		}
	}
	return free
}

// Ease is how far along the flow the handles of a plain S reach, as a share of the way it travels along
// it. Half gives an even S. One that runs more than eight times as far across as along turns
// sooner and crosses in a straighter line, down to a quarter at sixteen times. The layout uses
// this to tell how near two curves will pass, and the drawing to draw them.
func Ease(across, along float64) float64 {
	return math.Max(0.25, math.Min(0.5, (4*math.Abs(along))/or(math.Abs(across), 1)))
}

// Brings the labels and bends of each edge into line with an end of the edge, so that it runs
// straight from that end and turns once, at the other: with the end that has fewer edges beside
// it on its node, which is the one nearer the middle of its side, or else with the other, and
// as near to that as what is beside them in their ranks leaves room. The other end may then move
// along its node to meet the line. Bends in ranks that hold nothing else are left to `straighten`.
// An edge with no one line that all of its labels and bends have room on has each brought into
// line with the nearest point above or below that is held in place.
func alignLabels(g *Graph, nodes []*LNode, xs []float64, free [2][]int, opt LayeredOptions, empty []uint8) {
	sep := opt.EdgeSep
	n, real, maxRank, KIND, RANK, W := g.n, g.real, g.maxRank, g.KIND, g.RANK, g.W
	EF, ET, EFD, ETD := g.EF, g.ET, g.EFD, g.ETD
	succStart, succE, predStart, predE := g.succStart, g.succE, g.predStart, g.predE
	layerStart, order, pos := g.layerStart, g.order, g.pos
	placed := func(v int) bool {
		return KIND[v] == KindLabel || (KIND[v] == KindDummy && !(empty != nil && empty[RANK[v]] == 1))
	}
	// Where segment `e` ends on the way up (side 0) or down, and how many ends share that side of
	// the node there: none for a point that is not on a node.
	end := func(e int, side int) [2]float64 {
		u := ET[e]
		x := xs[u] + ETD[e]
		if side == 0 {
			u = EF[e]
			x = xs[u] + EFD[e]
		}
		c := 0
		if u < real && nodes[u].Kind == KindNode {
			c = free[1-side][u]
		}
		return [2]float64{x, float64(c)}
	}
	// The room the neighbours in its rank leave a label or bend: a full gap from a node or between
	// two labels, half of one between a label and a line that passes it.
	room := func(v int) (float64, float64) {
		s := layerStart[RANK[v]]
		i := s + pos[v]
		clear := func(other int) float64 {
			c := sep / 2
			if KIND[other] == KindNode {
				c = opt.NodeSep/2 + sep/2
			} else if KIND[other] == KIND[v] {
				c = sep
			}
			return W[other]/2 + W[v]/2 + c
		}
		lo, hi := negInf, inf
		if i > s {
			lo = xs[order[i-1]] + clear(order[i-1])
		}
		if i+1 < layerStart[RANK[v]+1] {
			hi = xs[order[i+1]] - clear(order[i+1])
		}
		return lo, hi
	}
	// The place between `lo` and `hi` that is in line with one of two ends, or else nearest the first choice.
	choose := func(x float64, up, down [2]float64, lo, hi float64) float64 {
		var upFirst bool
		if up[1] != down[1] {
			upFirst = up[1] < down[1]
		} else {
			upFirst = math.Abs(up[0]-x) <= math.Abs(down[0]-x)
		}
		choices := [2]float64{down[0], up[0]}
		if upFirst {
			choices = [2]float64{up[0], down[0]}
		}
		for _, choice := range choices {
			if choice >= lo && choice <= hi {
				return choice
			}
		}
		return math.Max(lo, math.Min(hi, choices[0]))
	}
	done := make([]uint8, n)
	var chain []int
	for r := 1; r < maxRank; r++ {
		for i := layerStart[r]; i < layerStart[r+1]; i++ {
			if !placed(order[i]) || done[order[i]] != 0 {
				continue
			}
			// The edge's labels and bends from its first node to its last, and the room they share.
			top := order[i]
			for isDummy(KIND[EF[predE[predStart[top]]]]) {
				top = EF[predE[predStart[top]]]
			}
			bottom := top
			lo := negInf
			hi := inf
			chain = chain[:0]
			for d := top; isDummy(KIND[d]); d = ET[succE[succStart[d]]] {
				bottom = d
				if !placed(d) {
					continue
				}
				done[d] = 1
				chain = append(chain, d)
				least, most := room(d)
				if least > lo {
					lo = least
				}
				if most < hi {
					hi = most
				}
			}
			if lo <= hi {
				x := choose(xs[chain[0]], end(predE[predStart[top]], 0), end(succE[succStart[bottom]], 1), lo, hi)
				for _, d := range chain {
					xs[d] = x
				}
				continue
			}
			for _, d := range chain {
				least, most := room(d)
				if least > most {
					continue
				}
				xs[d] = choose(xs[d], end(follow(g, empty, predE[predStart[d]], 0), 0), end(follow(g, empty, succE[succStart[d]], 1), 1), least, most)
			}
		}
	}
}

// Once the nodes have their places, moves an end along its side when that lets its edge run
// straight: when the point the edge runs to, or some part of the side it runs to, is in line
// with the end's own side. Any other end leans towards the point its edge runs to, where its
// side is long enough, and an end with a side to itself stays in the middle. On a group, the
// ends keep clear of the points where other edges pass through its border.
func settle(g *Graph, nodes []*LNode, xs []float64, EFK, ETK []End, free [2][]int, empty []uint8) {
	real, W, EF, ET, EFD, ETD := g.real, g.W, g.EF, g.ET, g.EFD, g.ETD
	succStart, succE, predStart, predE, pos := g.succStart, g.succE, g.predStart, g.predE, g.pos
	var ids []int
	var room []float64
	var all []int
	var sum []float64
	var count []float64
	// Where the end of segment `e` on node `v` should be, from the middle of its side. The edge is
	// followed up from the segment (side 0) or down, past the bends in ranks that hold nothing
	// else, to where it next comes to a point that is held in place or to another node's side.
	wanted := func(e int, side int, v int, span float64, again bool) float64 {
		if free[side][v] < 2 {
			return 0
		}
		e = follow(g, empty, e, side)
		u := ET[e]
		point := xs[u] + ETD[e]
		if side == 0 {
			u = EF[e]
			point = xs[u] + EFD[e]
		}
		// Once the other end has its place, this one comes into line with it if it can.
		if again && math.Abs(point-xs[v]) <= span/2+IN_LINE {
			return math.Max(-span/2, math.Min(span/2, point-xs[v]))
		}
		// The part of the other side that its end may move along, if it may move.
		middle := point
		width := 0.0
		kinds := ETK
		if side == 0 {
			kinds = EFK
		}
		if u < real && nodes[u].Kind == KindNode && kinds[e] != EndHeld && free[1-side][u] >= 2 {
			width = math.Max(0, math.Min(nodes[u].Span, W[u]))
			if width > 0 {
				middle = xs[u]
			}
		}
		lo := math.Max(xs[v]-span/2, middle-width/2)
		hi := math.Min(xs[v]+span/2, middle+width/2)
		// Two sides with no part in line: the end leans towards the other, if its side is long enough.
		if lo > hi+IN_LINE {
			lean := math.Max(0, span/2-LEAN_CLEAR)
			return math.Max(-lean, math.Min(lean, point-xs[v]))
		}
		// Both ends settle on one place in the part the two sides share: nearer the middle of the narrower.
		place := (xs[v]*width + middle*span) / (width + span)
		return math.Max(lo, math.Min(hi, place)) - xs[v]
	}
	// The ends of an edge follow each other, so the nodes are gone over until none moves: a few
	// times at most, in practice.
	moved := true
	for pass := 0; moved && pass < 6; pass++ {
		moved = false
		for v := 0; v < real; v++ {
			span := math.Min(nodes[v].Span, W[v])
			if span <= 0 || nodes[v].Kind != KindNode {
				continue
			}
			for side := 0; side < 2; side++ {
				kinds, dx, other, start, list := EFK, EFD, ET, succStart, succE
				if side == 0 {
					kinds, dx, other, start, list = ETK, ETD, EF, predStart, predE
				}
				sideEnds(g, v, side, span, kinds, &ids, &room)
				if len(ids) == 0 {
					continue
				}
				if len(ids) == 1 {
					if len(room) < 1 {
						room = append(room[:0], 0)
					}
					room[0] = 0
				}
				// Every end on the side in order, the held ones among them, which divide the rest into runs.
				all = all[:0]
				for k := start[v]; k < start[v+1]; k++ {
					all = append(all, list[k])
				}
				sortBy(all, func(a, b int) float64 { return or(float64(pos[other[a]]-pos[other[b]]), float64(a-b)) })
				k := 0
				lo := -span / 2
				for i := 0; i <= len(all); i++ {
					held := i < len(all) && kinds[all[i]] == EndHeld
					if i < len(all) && !held {
						continue
					}
					// The run of free ends before this point is ids[k..], as many as were passed since the last held end.
					size := 0
					for j := i - 1; j >= 0 && kinds[all[j]] != EndHeld; j-- {
						size++
					}
					hi := span / 2
					if held {
						hi = dx[all[i]] - PORT_PITCH
					}
					if size > 0 {
						// The places nearest the wanted ones that keep the order and the room between
						// neighbours: an isotonic fit, by pooling neighbours that would otherwise be too close.
						sum = sum[:0]
						count = count[:0]
						before := 0.0
						for j := 0; j < size; j++ {
							if j > 0 {
								before += room[k+j]
							}
							sum = append(sum, wanted(ids[k+j], side, v, span, pass > 0)-before)
							count = append(count, 1)
							// Ends that share a place for want of room stay together.
							together := j > 0 && room[k+j] == 0
							for len(sum) > 1 && (together || sum[len(sum)-2]/count[len(count)-2] > sum[len(sum)-1]/count[len(count)-1]) {
								together = false
								top := sum[len(sum)-1]
								sum = sum[:len(sum)-1]
								pooled := count[len(count)-1]
								count = count[:len(count)-1]
								sum[len(sum)-1] += top
								count[len(count)-1] += pooled
							}
						}
						total := before
						j := 0
						before = 0
						for b := 0; b < len(sum); b++ {
							// With too little room between two held ends, the run sits in the middle of what there is.
							var level float64
							if hi-total < lo {
								level = (lo + hi - total) / 2
							} else {
								level = math.Max(lo, math.Min(hi-total, sum[b]/count[b]))
							}
							for c := 0; c < int(count[b]); c, j = c+1, j+1 {
								if j > 0 {
									before += room[k+j]
								}
								if math.Abs(dx[ids[k+j]]-level-before) > 0.01 {
									moved = true
								}
								dx[ids[k+j]] = level + before
							}
						}
						k += size
					}
					if held {
						lo = dx[all[i]] + PORT_PITCH
					}
				}
			}
		}
	}
}

// Brings edges that are nearly straight into line, by moving a node a little across the flow
// along with everything it is in line with already, where the nodes beside them leave room.
// What is in line stays in line, so each move leaves one more edge straight.
func snap(g *Graph, xs []float64, empty []uint8, opt LayeredOptions) {
	n, KIND, RANK, EF, ET, EFD, ETD := g.n, g.KIND, g.RANK, g.EF, g.ET, g.EFD, g.ETD
	layerStart, order, pos := g.layerStart, g.order, g.pos
	passed := func(u int) bool { return empty != nil && KIND[u] == KindDummy && empty[RANK[u]] == 1 }
	// Each edge from one point that is held in place to the next, past the bends in ranks that hold nothing else.
	var a, b []int
	var da, db []float64
	for e := 0; e < len(EF); e++ {
		if passed(EF[e]) {
			continue
		}
		f := follow(g, empty, e, 1)
		a = append(a, EF[e])
		b = append(b, ET[f])
		da = append(da, EFD[e])
		db = append(db, ETD[f])
	}
	off := func(k int) float64 { return xs[b[k]] + db[k] - xs[a[k]] - da[k] }
	// The nodes in line with each other move as one.
	block := make([]int, n)
	members := make([][]int, n)
	for i := 0; i < n; i++ {
		block[i] = i
		members[i] = []int{i}
	}
	join := func(x, y int) {
		keep := block[x]
		drop := block[y]
		if keep == drop {
			return
		}
		if len(members[keep]) < len(members[drop]) {
			keep, drop = drop, keep
		}
		for _, v := range members[drop] {
			block[v] = keep
			members[keep] = append(members[keep], v)
		}
		members[drop] = nil
	}
	var near []int
	for k := 0; k < len(a); k++ {
		d := math.Abs(off(k))
		if d < 0.01 {
			join(a[k], b[k])
		} else if d <= SNAP {
			near = append(near, k)
		}
	}
	if len(near) == 0 {
		return
	}
	sortBy(near, func(x, y int) float64 { return or(math.Abs(off(x))-math.Abs(off(y)), float64(x-y)) })
	gap := func(v int) float64 { return halfRoom(g, opt, v) }
	// Whether every node of a block can move by `shift` and keep its room from those beside it.
	free := func(id int, shift float64) bool {
		for _, v := range members[id] {
			s := layerStart[RANK[v]]
			end := layerStart[RANK[v]+1]
			i := s + pos[v]
			if i > s && block[order[i-1]] != id && xs[v]+shift-xs[order[i-1]] < gap(v)+gap(order[i-1])-0.01 {
				return false
			}
			if i+1 < end && block[order[i+1]] != id && xs[order[i+1]]-xs[v]-shift < gap(v)+gap(order[i+1])-0.01 {
				return false
			}
		}
		return true
	}
	for _, k := range near {
		d := off(k)
		if math.Abs(d) < 0.01 {
			join(a[k], b[k])
			continue
		}
		if math.Abs(d) > SNAP || block[a[k]] == block[b[k]] {
			continue
		}
		// The smaller of the two blocks moves, if it can; else the other.
		upper := block[a[k]]
		lower := block[b[k]]
		first := lower
		if len(members[upper]) <= len(members[lower]) {
			first = upper
		}
		second := upper
		if first == upper {
			second = lower
		}
		for _, id := range [2]int{first, second} {
			shift := -d
			if id == upper {
				shift = d
			}
			if !free(id, shift) {
				continue
			}
			for _, v := range members[id] {
				xs[v] += shift
			}
			join(a[k], b[k])
			break
		}
	}
}

// Marks the ranks that hold nothing but the bends of edges, if there are any.
func emptyRanks(g *Graph) []uint8 {
	empty := make([]uint8, g.maxRank+1)
	any := false
	for r := 1; r < g.maxRank; r++ {
		only := g.layerStart[r+1] > g.layerStart[r]
		for i := g.layerStart[r]; only && i < g.layerStart[r+1]; i++ {
			if g.KIND[g.order[i]] != KindDummy {
				only = false
			}
		}
		if only {
			empty[r] = 1
			any = true
		}
	}
	if any {
		return empty
	}
	return nil
}

// orderedSet is a Set that keeps insertion order.
type orderedSet struct {
	items []int
	has   map[int]bool
}

func newOrderedSet(items []int) *orderedSet {
	s := &orderedSet{has: map[int]bool{}}
	for _, k := range items {
		s.add(k)
	}
	return s
}

func (s *orderedSet) add(k int) *orderedSet {
	if !s.has[k] {
		s.has[k] = true
		s.items = append(s.items, k)
	}
	return s
}

// Gives every piece that does not run straight down a track across its gap, and counts the
// tracks of each gap. Two pieces in one gap share a track only where they cannot be mistaken
// for each other: where they do not overlap, or where they leave from or arrive at the same
// point, as the branches of a tree do. Which of two goes above the other is whichever makes
// them cross less. With `curved`, the tracks are levels for curves to turn onto, and only the
// pieces that would run along or too near another get one: each group of those counts its
// tracks from its own first. `room` is the height of the gap under each rank, and `tall`
// receives the height that a gap whose curves mostly cross each other should have instead of levels.
func assignTracks(pieces *Pieces, trackCount []int, curved bool, room []float64, tall []float64) {
	from, to, rank, wide := pieces.from, pieces.to, pieces.rank, pieces.wide
	total := len(from)
	var byRankKeys []int
	byRank := map[int][]int{}
	for k := 0; k < total; k++ {
		pieces.track = append(pieces.track, -1)
		pieces.levels = append(pieces.levels, 0)
		if math.Abs(from[k]-to[k]) < IN_LINE {
			continue
		}
		if _, ok := byRank[rank[k]]; !ok {
			byRankKeys = append(byRankKeys, rank[k])
		}
		byRank[rank[k]] = append(byRank[rank[k]], k)
	}
	track, levels := pieces.track, pieces.levels
	// The pieces are compared pair by pair, so where each begins and ends across the flow is worked out once.
	LO := make([]float64, total)
	HI := make([]float64, total)
	for k := 0; k < total; k++ {
		LO[k] = math.Min(from[k], to[k])
		HI[k] = math.Max(from[k], to[k])
	}
	within := func(x float64, k int) int {
		if x >= LO[k]-IN_LINE && x <= HI[k]+IN_LINE {
			return 1
		}
		return 0
	}
	mates := func(a, b int) bool { return math.Abs(from[a]-from[b]) < IN_LINE || math.Abs(to[a]-to[b]) < IN_LINE }
	overlap := func(a, b int) bool { return LO[a] < HI[b]-IN_LINE && LO[b] < HI[a]-IN_LINE }
	// Whether a and b need tracks of their own.
	clash := func(a, b int) bool { return overlap(a, b) && !mates(a, b) }
	// Whether one of a and b could cross the other, depending on which is above.
	meet := func(a, b int) bool {
		return overlap(a, b) || within(to[a], b)+within(from[b], a)+within(to[b], a)+within(from[a], b) > 0
	}
	// How often a and b cross with a on the higher track: a's way down through b, and b's way down to itself through a.
	crossings := func(a, b int) int { return within(to[a], b) + within(from[b], a) }

	// Gives the pieces of one gap in `list` their tracks, and each the number of tracks that the
	// pieces it has to do with take between them, counting its own from the first of those.
	// Returns the number of tracks. The pieces come without tracks.
	lay := func(list []int) int {
		// A plain order to start from, and to keep where nothing says otherwise: the pieces that
		// reach furthest go first, so that those of one fan nest.
		sortBy(list, func(a, b int) float64 {
			return or(or(HI[b]-LO[b]-(HI[a]-LO[a]), LO[a]-LO[b]), float64(a-b))
		})
		count := len(list)
		var above [][]int
		group := make([]int, count)
		for i := range group {
			group[i] = i
		}
		find := func(i int) int {
			for group[i] != i {
				g := group[group[i]]
				group[i] = g
				i = g
			}
			return i
		}
		sequence := list
		if count <= MAX_WEIGHED {
			// above[j] lists the pieces that should be above list[j]. The order follows as many of
			// these as it can: a piece is placed once all that should be above it are, and when
			// none is free, the one with the fewest still waiting.
			waiting := make([]int, count)
			after := make([][]int, count)
			above = make([][]int, count)
			for i := 0; i < count; i++ {
				for j := i + 1; j < count; j++ {
					a := list[i]
					b := list[j]
					if !meet(a, b) {
						continue
					}
					fi := find(i)
					group[fi] = find(j)
					ab := crossings(a, b)
					ba := crossings(b, a)
					if ab == ba {
						continue
					}
					first, second := j, i
					if ab < ba {
						first, second = i, j
					}
					above[second] = append(above[second], first)
					after[first] = append(after[first], second)
					waiting[second]++
				}
			}
			placed := make([]uint8, count)
			result := make([]int, 0, count)
			for done := 0; done < count; done++ {
				pick := -1
				for i := 0; i < count; i++ {
					if placed[i] != 0 {
						continue
					}
					if pick < 0 || waiting[i] < waiting[pick] {
						pick = i
					}
					if waiting[i] == 0 {
						break
					}
				}
				placed[pick] = 1
				result = append(result, pick)
				for _, j := range after[pick] {
					waiting[j]--
				}
			}
			sequence = make([]int, count)
			for i, r := range result {
				sequence[i] = list[r]
			}
		}
		// Each piece takes the first track below those it should be under that has room for it.
		place := map[int]int{}
		for i, k := range list {
			place[k] = i
		}
		var rows [][]int
		for _, k := range sequence {
			t := 0
			if above != nil {
				for _, i := range above[place[k]] {
					if track[list[i]] >= t {
						t = track[list[i]] + 1
					}
				}
			}
			for ; ; t++ {
				for len(rows) <= t {
					rows = append(rows, nil)
				}
				free := true
				for _, other := range rows[t] {
					if clash(k, other) {
						free = false
					}
				}
				if free {
					rows[t] = append(rows[t], k)
					break
				}
			}
			track[k] = t
		}
		first := map[int]int{}
		lastOf := map[int]int{}
		for i, k := range list {
			root := find(i)
			if f, ok := first[root]; !ok || track[k] < f {
				first[root] = track[k]
			}
			if l, ok := lastOf[root]; !ok || track[k] > l {
				lastOf[root] = track[k]
			}
		}
		for i, k := range list {
			root := find(i)
			levels[k] = lastOf[root] - first[root] + 1
			if curved {
				track[k] -= first[root]
			}
		}
		return len(rows)
	}

	// The least distance between a and b as plain curves across a gap that leaves them `height`
	// to turn in: between points along each and the other drawn as a run of short lines.
	const SAMPLES = 16
	sample := func(k int, h float64, out []float64) {
		c := Ease(to[k]-from[k], h)
		for i := 0; i <= SAMPLES; i++ {
			t := float64(i) / SAMPLES
			u := 1 - t
			out[2*i] = from[k] + (to[k]-from[k])*t*t*(3-2*t)
			out[2*i+1] = h * (3*c*t*u*u + 3*(1-c)*t*t*u + t*t*t)
		}
	}
	lineA := make([]float64, 2*SAMPLES+2)
	lineB := make([]float64, 2*SAMPLES+2)
	reach := func(points, line []float64) float64 {
		least := inf
		for i := 0; i <= SAMPLES; i++ {
			px := points[2*i]
			py := points[2*i+1]
			for j := 0; j < SAMPLES; j++ {
				dx := line[2*j+2] - line[2*j]
				dy := line[2*j+3] - line[2*j+1]
				t := math.Max(0, math.Min(1, ((px-line[2*j])*dx+(py-line[2*j+1])*dy)/or(dx*dx+dy*dy, 1)))
				d := js.Hypot(px-line[2*j]-t*dx, py-line[2*j+1]-t*dy)
				if d < least {
					least = d
				}
			}
		}
		return least
	}
	nearest := func(a, b int, height float64) float64 {
		h := math.Max(height/2, height-2*LEVEL_LEAD)
		sample(a, h, lineA)
		sample(b, h, lineB)
		return math.Min(reach(lineA, lineB), reach(lineB, lineA))
	}

	for _, r := range byRankKeys {
		all := byRank[r]
		if !curved {
			trackCount[r] = lay(all)
			continue
		}
		if len(all) > MAX_WEIGHED {
			continue
		}
		// The pieces that turn onto levels: first those that run far across and have another such
		// piece to be told apart from, then any two that would pass too near each other as plain
		// curves.
		var far []int
		for _, k := range all {
			if wide[k] == 1 {
				far = append(far, k)
			}
		}
		lay(far)
		var many []int
		for _, k := range far {
			if levels[k] > 1 {
				many = append(many, k)
			}
		}
		level := newOrderedSet(many)
		for i := 0; i < len(all); i++ {
			for j := i + 1; j < len(all); j++ {
				a := all[i]
				b := all[j]
				if (level.has[a] && level.has[b]) || !clash(a, b) || (from[a] < to[a]) != (from[b] < to[b]) {
					continue
				}
				limit := float64(LEVEL_TOUCH)
				if wide[a] == 1 || wide[b] == 1 {
					limit = LEVEL_CLEAR
				}
				if nearest(a, b, room[r]) < limit {
					level.add(a).add(b)
				}
			}
		}
		for _, k := range far {
			track[k] = -1
			levels[k] = 0
		}
		if len(level.items) == 0 {
			continue
		}
		crossing := 0
		alongside := 0
		turning := slices.Clone(level.items)
		for i := 0; i < len(turning); i++ {
			for j := i + 1; j < len(turning); j++ {
				a := turning[i]
				b := turning[j]
				if !overlap(a, b) {
					continue
				}
				if (from[a] < to[a]) != (from[b] < to[b]) {
					crossing++
				} else if !mates(a, b) {
					alongside++
				}
			}
		}
		if crossing > alongside {
			for _, k := range all {
				if (HI[k]-LO[k])*CROSSED_SLOPE > tall[r] {
					tall[r] = (HI[k] - LO[k]) * CROSSED_SLOPE
				}
			}
			continue
		}
		// With them go the short pieces that could cross one of them, so that each has its place
		// among the levels and crosses none.
		beside := func(k int) bool {
			for _, j := range level.items {
				if meet(k, j) {
					return true
				}
			}
			return false
		}
		var members []int
		for _, k := range all {
			if level.has[k] || (wide[k] != 1 && beside(k)) {
				members = append(members, k)
			}
		}
		lay(members)
		most := 0
		for _, k := range members {
			if levels[k] > most {
				most = levels[k]
			}
		}
		trackCount[r] = most
	}
}

// Moves the bends in ranks that hold only bends onto the straight line between the nearest points
// of their edges that are held in place, and marks the ones that can be left out of the route.
// `firstDummy` and `span` give each edge's run of bends, one in every rank. `sep` is the room
// kept between the bends of twin edges, or 0 to leave those out as well.
func straighten(g *Graph, xs []float64, empty []uint8, firstDummy []int, span []int, sep float64, onLine []uint8) {
	EF, ET, EFD, ETD := g.EF, g.ET, g.EFD, g.ETD
	succStart, succE, predStart, predE, layerStart, order := g.succStart, g.succE, g.predStart, g.predE, g.layerStart, g.order

	want := make([]float64, len(xs))
	// The points that each bend's edge runs between, across the ranks that hold only bends.
	from := make([]float64, len(xs))
	to := make([]float64, len(xs))
	for ei := 0; ei < len(firstDummy); ei++ {
		d0 := firstDummy[ei]
		if d0 < 0 {
			continue
		}
		end := d0 + span[ei] - 1
		// A bend's rank is its place in the run, counted from the rank after the edge's first node.
		base := g.RANK[d0]
		for d := d0; d < end; d++ {
			if empty[base+d-d0] == 0 {
				continue
			}
			last := d
			for last+1 < end && empty[base+last+1-d0] != 0 {
				last++
			}
			above := predE[predStart[d]]
			below := succE[succStart[last]]
			x0 := xs[EF[above]] + EFD[above]
			x1 := xs[ET[below]] + ETD[below]
			steps := float64(last - d + 2)
			for k := d; k <= last; k++ {
				want[k] = x0 + ((x1-x0)*float64(k-d+1))/steps
				from[k] = x0
				to[k] = x1
			}
			d = last
		}
	}

	// Nothing in such a rank is in a bend's way, so each one goes onto its line and out of the
	// route. Only twins stay: bends of edges that run between the same two points, which would
	// otherwise be drawn as one. They take places a little apart, around the line.
	var twins []int
	for r := 1; r < g.maxRank; r++ {
		if empty[r] == 0 {
			continue
		}
		row := slices.Clone(order[layerStart[r]:layerStart[r+1]])
		sortBy(row, func(a, b int) float64 { return or(or(from[a]-from[b], to[a]-to[b]), float64(a-b)) })
		for i := 0; i < len(row); {
			twins = twins[:0]
			j := i
			for ; j < len(row) && math.Abs(from[row[j]]-from[row[i]]) < 1 && math.Abs(to[row[j]]-to[row[i]]) < 1; j++ {
				twins = append(twins, row[j])
			}
			// The twins keep the order the rank gave them, so that two edges do not swap sides on the way.
			sortBy(twins, func(a, b int) float64 { return float64(g.pos[a] - g.pos[b]) })
			for k := 0; k < len(twins); k++ {
				d := twins[k]
				xs[d] = want[d] + (float64(k)-float64(len(twins)-1)/2)*sep
				if len(twins) == 1 || sep == 0 {
					onLine[d] = 1
				}
			}
			i = j
		}
	}
}

// In a rank that holds labels and bends but no nodes, a bend is there to take its edge straight
// through between the labels. Where the edge would pass clear of every label anyway, the bend
// is left out, and the edge turns in the whole height between the ranks on either side.
func pastLabels(g *Graph, xs []float64, firstDummy []int, span []int, clear float64, onLine []uint8) {
	maxRank, W, KIND, RANK, EF, ET, EFD, ETD := g.maxRank, g.W, g.KIND, g.RANK, g.EF, g.ET, g.EFD, g.ETD
	succStart, succE, predStart, predE, layerStart, order := g.succStart, g.succE, g.predStart, g.predE, g.layerStart, g.order
	labelled := make([]uint8, maxRank+1)
	any := false
	for r := 1; r < maxRank; r++ {
		labels := false
		others := false
		for i := layerStart[r]; i < layerStart[r+1]; i++ {
			kind := KIND[order[i]]
			if kind == KindLabel {
				labels = true
			} else if kind != KindDummy {
				others = true
			}
		}
		if labels && !others {
			labelled[r] = 1
			any = true
		}
	}
	if !any {
		return
	}
	for ei := 0; ei < len(firstDummy); ei++ {
		d0 := firstDummy[ei]
		if d0 < 0 {
			continue
		}
		for d, end := d0, d0+span[ei]-1; d < end; d++ {
			if KIND[d] != KindDummy || labelled[RANK[d]] == 0 {
				continue
			}
			// The points on either side that the edge is held at, past any bends already left out.
			first := d
			last := d
			for first > d0 && onLine[first-1] != 0 {
				first--
			}
			for last+1 < end && onLine[last+1] != 0 {
				last++
			}
			above := predE[predStart[first]]
			below := succE[succStart[last]]
			from := xs[EF[above]] + EFD[above]
			to := xs[ET[below]] + ETD[below]
			lo := math.Min(from, to) - clear
			hi := math.Max(from, to) + clear
			free := true
			r := RANK[d]
			for i := layerStart[r]; free && i < layerStart[r+1]; i++ {
				label := order[i]
				if KIND[label] == KindLabel && xs[label]+W[label]/2 > lo && xs[label]-W[label]/2 < hi {
					free = false
				}
			}
			if !free {
				continue
			}
			onLine[d] = 1
			// The bends left out between the two points lie on the line between them.
			steps := float64(last - first + 2)
			for k := first; k <= last; k++ {
				xs[k] = from + ((to-from)*float64(k-first+1))/steps
			}
		}
	}
}

func reduceCrossings(g *Graph, nodes []*LNode) {
	n, real, maxRank, W, EF, ET, EFD, ETD := g.n, g.real, g.maxRank, g.W, g.EF, g.ET, g.EFD, g.ETD
	succStart, succE, predStart, predE, layerStart, order, pos, offsets := g.succStart, g.succE, g.predStart, g.predE, g.layerStart, g.order, g.pos, g.offsets
	widest := 0
	for r := 0; r <= maxRank; r++ {
		widest = max(widest, layerStart[r+1]-layerStart[r])
	}
	// With offsets, edges that reach one node at different offsets take separate places along it.
	var place []int
	if offsets {
		place = make([]int, len(EF))
	}
	size := 1
	limit := widest
	if place != nil {
		limit = widest + len(EF)
	}
	for size < limit {
		size <<= 1
	}
	tree := make([]int, 2*size)
	targets := make([]int, len(EF))

	count := func() int {
		total := 0
		for r := 0; r < maxRank; r++ {
			southLen := layerStart[r+2] - layerStart[r+1]
			if place != nil {
				places := 0
				for i := layerStart[r+1]; i < layerStart[r+2]; i++ {
					from := predStart[order[i]]
					to := predStart[order[i]+1]
					mixed := false
					for k := from + 1; k < to; k++ {
						if ETD[predE[k]] != ETD[predE[from]] {
							mixed = true
						}
					}
					if mixed {
						ids := slices.Clone(predE[from:to])
						sortBy(ids, func(a, b int) float64 { return ETD[a] - ETD[b] })
						for k := 0; k < len(ids); k++ {
							if k > 0 && ETD[ids[k]] != ETD[ids[k-1]] {
								places++
							}
							place[ids[k]] = places
						}
					} else {
						for k := from; k < to; k++ {
							place[predE[k]] = places
						}
					}
					places++
				}
				southLen = places
			}
			if southLen < 2 {
				continue
			}
			t := 0
			for i := layerStart[r]; i < layerStart[r+1]; i++ {
				v := order[i]
				from := succStart[v]
				to := succStart[v+1]
				t0 := t
				for k := from; k < to; k++ {
					if place != nil {
						targets[t] = place[succE[k]]
					} else {
						targets[t] = pos[ET[succE[k]]]
					}
					t++
				}
				d := t - t0
				if d < 2 {
					continue
				}
				mixed := false
				if place != nil {
					for k := from + 1; k < to; k++ {
						if EFD[succE[k]] != EFD[succE[from]] {
							mixed = true
						}
					}
				}
				if place != nil && mixed {
					ids := slices.Clone(succE[from:to])
					sortBy(ids, func(a, b int) float64 { return or(EFD[a]-EFD[b], float64(place[a]-place[b])) })
					for k := 0; k < d; k++ {
						targets[t0+k] = place[ids[k]]
					}
				} else if d <= 12 {
					for a := t0 + 1; a < t; a++ {
						item := targets[a]
						b := a - 1
						for b >= t0 && targets[b] > item {
							targets[b+1] = targets[b]
							b--
						}
						targets[b+1] = item
					}
				} else {
					slices.Sort(targets[t0:t])
				}
			}
			if t < 2 {
				continue
			}
			leaves := 1
			for leaves < southLen {
				leaves <<= 1
			}
			clear(tree[:2*leaves])
			for k := 0; k < t; k++ {
				i := targets[k] + leaves
				tree[i]++
				for i > 1 {
					if i&1 == 0 {
						total += tree[i+1]
					}
					i >>= 1
					tree[i]++
				}
			}
		}
		return total
	}

	best := count()
	if best == 0 {
		return
	}
	bestOrder := slices.Clone(order)
	bary := make([]float64, n)
	moving := make([]int, widest)
	leftBias := func(a, b int) float64 { return or(bary[a]-bary[b], float64(pos[a]-pos[b])) }
	rightBias := func(a, b int) float64 { return or(bary[a]-bary[b], float64(pos[b]-pos[a])) }

	sweep := func(down bool, biasRight bool) {
		start, list, other, otherDx := succStart, succE, ET, ETD
		if down {
			start, list, other, otherDx = predStart, predE, EF, EFD
		}
		for step := 1; step <= maxRank; step++ {
			r := maxRank - step
			if down {
				r = step
			}
			s := layerStart[r]
			e := layerStart[r+1]
			movable := 0
			for i := s; i < e; i++ {
				v := order[i]
				pin := 0.0
				if v < real {
					pin = nodes[v].Pin
				}
				if pin != 0 {
					bary[v] = pin * 1e9
					moving[movable] = v
					movable++
					continue
				}
				from := start[v]
				to := start[v+1]
				if from == to {
					bary[v] = -1
					continue
				}
				sum := 0.0
				for k := from; k < to; k++ {
					u := other[list[k]]
					sum += float64(pos[u])
					if offsets && W[u] > 0 {
						sum += otherDx[list[k]] / W[u]
					}
				}
				bary[v] = sum / float64(to-from)
				moving[movable] = v
				movable++
			}
			if movable < 2 {
				continue
			}
			if biasRight {
				sortBy(moving[:movable], rightBias)
			} else {
				sortBy(moving[:movable], leftBias)
			}
			k := 0
			for i := s; i < e; i++ {
				if bary[order[i]] != -1 {
					order[i] = moving[k]
					k++
				}
			}
			for i := s; i < e; i++ {
				pos[order[i]] = i - s
			}
		}
	}

	// How many pairs of nodes in one rank are not in the order they were given.
	outOfOrder := func() int {
		total := 0
		for r := 0; r <= maxRank; r++ {
			for i := layerStart[r]; i < layerStart[r+1]; i++ {
				if order[i] >= real {
					continue
				}
				for j := i + 1; j < layerStart[r+1]; j++ {
					if order[j] < order[i] {
						total++
					}
				}
			}
		}
		return total
	}
	settlePositions := func() {
		for r := 0; r <= maxRank; r++ {
			for i := layerStart[r]; i < layerStart[r+1]; i++ {
				pos[order[i]] = i - layerStart[r]
			}
		}
	}

	// The sweeps are run twice from the same start, going down first and going up first. They
	// often end with as few crossings either way but a different order, and then the one that
	// keeps more of the nodes in the order they were given is the less surprising.
	// Only for graphs small enough to be read node by node: a second run costs as much as the first.
	initial := slices.Clone(order)
	start := best
	small := n <= 600
	chosen := bestOrder
	chosenOut := math.MaxInt
	attempts := 1
	if small {
		attempts = 2
	}
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			copy(order, initial)
			settlePositions()
		}
		found := start
		foundOrder := slices.Clone(initial)
		for i, stale := 0, 0; stale < 4 && i < MAX_SWEEPS; i, stale = i+1, stale+1 {
			sweep((i+attempt)%2 == 0, i%4 >= 2)
			c := count()
			if c < found {
				found = c
				copy(foundOrder, order)
				stale = 0
				if c == 0 {
					break
				}
			}
		}
		copy(order, foundOrder)
		out := 0
		if small {
			out = outOfOrder()
		}
		if attempt == 0 || found < best || (found == best && out < chosenOut) {
			best = found
			chosen = foundOrder
			chosenOut = out
		}
	}
	copy(order, chosen)
	settlePositions()
	copy(bestOrder, chosen)

	// A drawing and its mirror image cross as often too. Of those two, likewise.
	if !small {
		return
	}
	for i := 0; i < real; i++ {
		if nodes[i].Pin != 0 {
			return
		}
	}
	if chosenOut == 0 {
		return
	}
	for r := 0; r <= maxRank; r++ {
		slices.Reverse(order[layerStart[r]:layerStart[r+1]])
	}
	settlePositions()
	if count() > best || outOfOrder() >= chosenOut {
		copy(order, bestOrder)
	}
}

// Half the room a node, bend or port takes across the flow, with its share of the gap beside it.
// A port on a group's border is a point that an edge passes through, and needs no more than a bend.
func halfRoom(g *Graph, opt LayeredOptions, v int) float64 {
	sep := opt.EdgeSep
	if g.KIND[v] == KindNode {
		sep = opt.NodeSep
	}
	return g.W[v]/2 + sep/2
}

// Brandes-Köpf horizontal coordinate assignment: four aligned layouts, balanced.
func position(g *Graph, opt LayeredOptions) []float64 {
	n, maxRank, W, KIND, EF, ET, EFD, ETD := g.n, g.maxRank, g.W, g.KIND, g.EF, g.ET, g.EFD, g.ETD
	succStart, succE, predStart, predE, layerStart, order, pos := g.succStart, g.succE, g.predStart, g.predE, g.layerStart, g.order, g.pos
	segments := len(EF)
	gap := make([]float64, n)
	for i := 0; i < n; i++ {
		gap[i] = halfRoom(g, opt, i)
	}

	// Neighbours in left-to-right order, so the medians can be read off directly.
	key := make([]int, segments)
	for e := 0; e < segments; e++ {
		key[e] = pos[ET[e]]
	}
	for v := 0; v < n; v++ {
		if succStart[v+1]-succStart[v] > 1 {
			sortRangeInt(succE, succStart[v], succStart[v+1], key)
		}
	}
	for e := 0; e < segments; e++ {
		key[e] = pos[EF[e]]
	}
	for v := 0; v < n; v++ {
		if predStart[v+1]-predStart[v] > 1 {
			sortRangeInt(predE, predStart[v], predStart[v+1], key)
		}
	}

	// An edge between two real nodes may not cross a dummy-to-dummy segment; mark those that would.
	conflict := make([]uint8, segments)
	for r := 1; r <= maxRank; r++ {
		prevLen := layerStart[r] - layerStart[r-1]
		s := layerStart[r]
		end := layerStart[r+1]
		k0 := 0
		scan := s
		for i := s; i < end; i++ {
			v := order[i]
			inner := -1
			if isDummy(KIND[v]) {
				u := EF[predE[predStart[v]]]
				if isDummy(KIND[u]) {
					inner = u
				}
			}
			k1 := prevLen
			if inner >= 0 {
				k1 = pos[inner]
			}
			if inner >= 0 || i == end-1 {
				for j := scan; j <= i; j++ {
					w := order[j]
					wDummy := isDummy(KIND[w])
					for k := predStart[w]; k < predStart[w+1]; k++ {
						e := predE[k]
						up := pos[EF[e]]
						if (up < k0 || k1 < up) && !(wDummy && isDummy(KIND[EF[e]])) {
							conflict[e] = 1
						}
					}
				}
				scan = i + 1
				k0 = k1
			}
		}
	}

	var results [][]float64
	root := make([]int, n)
	align := make([]int, n)
	// Offset of each node from its block's root, so that aligned edges meet at their attachment points.
	shift := make([]float64, n)
	eFrom := make([]int, n)
	eTo := make([]int, n)
	eW := make([]float64, n)
	indeg := make([]int, n)
	outStart := make([]int, n+1)
	outE := make([]int, n)
	fill := make([]int, n)
	topo := make([]int, n)

	for variant := 0; variant < 4; variant++ {
		up := variant < 2
		right := variant&1 == 1
		start, list, other, ownDx, otherDx := succStart, succE, ET, EFD, ETD
		if up {
			start, list, other, ownDx, otherDx = predStart, predE, EF, ETD, EFD
		}
		for i := 0; i < n; i++ {
			root[i] = i
			align[i] = i
			shift[i] = 0
		}

		for step := 0; step <= maxRank; step++ {
			r := maxRank - step
			if up {
				r = step
			}
			s := layerStart[r]
			end := layerStart[r+1]
			adjacent := r + 1
			if up {
				adjacent = r - 1
			}
			adjacentLast := 0
			if adjacent >= 0 && adjacent <= maxRank {
				adjacentLast = layerStart[adjacent+1] - layerStart[adjacent] - 1
			}
			prevIdx := -1
			i := s
			if right {
				i = end - 1
			}
			for (right && i >= s) || (!right && i < end) {
				v := order[i]
				from := start[v]
				d := start[v+1] - from
				if d != 0 {
					lo := from + ((d - 1) >> 1)
					hi := from + (d >> 1)
					for pass := 0; pass < 2; pass++ {
						if pass == 1 && lo == hi {
							break
						}
						idxE := hi
						if (pass == 0) != right {
							idxE = lo
						}
						e := list[idxE]
						w := other[e]
						idx := pos[w]
						if right {
							idx = adjacentLast - pos[w]
						}
						if align[v] == v && prevIdx < idx && conflict[e] == 0 {
							align[w] = v
							root[v] = root[w]
							align[v] = root[v]
							shift[v] = shift[w] + otherDx[e] - ownDx[e]
							prevIdx = idx
						}
					}
				}
				if right {
					i--
				} else {
					i++
				}
			}
		}

		blockEdges := 0
		clear(indeg)
		clear(outStart)
		for r := 0; r <= maxRank; r++ {
			s := layerStart[r]
			end := layerStart[r+1]
			for i := s + 1; i < end; i++ {
				a, b := order[i-1], order[i]
				if right {
					a, b = order[i], order[i-1]
				}
				sep := gap[a] + gap[b]
				eFrom[blockEdges] = root[a]
				eTo[blockEdges] = root[b]
				if right {
					eW[blockEdges] = sep - shift[a] + shift[b]
				} else {
					eW[blockEdges] = sep + shift[a] - shift[b]
				}
				indeg[root[b]]++
				outStart[root[a]+1]++
				blockEdges++
			}
		}
		for i := 0; i < n; i++ {
			outStart[i+1] += outStart[i]
		}
		copy(fill, outStart[:n])
		for e := 0; e < blockEdges; e++ {
			outE[fill[eFrom[e]]] = e
			fill[eFrom[e]]++
		}

		x := make([]float64, n)
		count := 0
		for i := 0; i < n; i++ {
			if root[i] == i && indeg[i] == 0 {
				topo[count] = i
				count++
			}
		}
		for q := 0; q < count; q++ {
			v := topo[q]
			for k := outStart[v]; k < outStart[v+1]; k++ {
				e := outE[k]
				w := eTo[e]
				if x[v]+eW[e] > x[w] {
					x[w] = x[v] + eW[e]
				}
				indeg[w]--
				if indeg[w] == 0 {
					topo[count] = w
					count++
				}
			}
		}
		for q := count - 1; q >= 0; q-- {
			v := topo[q]
			min := inf
			for k := outStart[v]; k < outStart[v+1]; k++ {
				e := outE[k]
				cand := x[eTo[e]] - eW[e]
				if cand < min {
					min = cand
				}
			}
			if min != inf && min > x[v] {
				x[v] = min
			}
		}
		xs := make([]float64, n)
		for i := 0; i < n; i++ {
			if right {
				xs[i] = -x[root[i]] + shift[i]
			} else {
				xs[i] = x[root[i]] + shift[i]
			}
		}
		results = append(results, xs)
	}

	narrowest := 0
	bestWidth := inf
	var mins, maxs []float64
	for k := 0; k < 4; k++ {
		lo := inf
		hi := negInf
		xs := results[k]
		for i := 0; i < n; i++ {
			half := W[i] / 2
			if xs[i]-half < lo {
				lo = xs[i] - half
			}
			if xs[i]+half > hi {
				hi = xs[i] + half
			}
		}
		mins = append(mins, lo)
		maxs = append(maxs, hi)
		if hi-lo < bestWidth {
			bestWidth = hi - lo
			narrowest = k
		}
	}
	for k := 0; k < 4; k++ {
		delta := maxs[narrowest] - maxs[k]
		if k&1 == 0 {
			delta = mins[narrowest] - mins[k]
		}
		if delta != 0 {
			xs := results[k]
			for i := 0; i < n; i++ {
				xs[i] += delta
			}
		}
	}

	out := make([]float64, n)
	for i := 0; i < n; i++ {
		a := results[0][i]
		b := results[1][i]
		c := results[2][i]
		d := results[3][i]
		if a > b {
			a, b = b, a
		}
		if c > d {
			c, d = d, c
		}
		// Only b and c are read below: the larger of a and c, the smaller of b and d.
		if a > c {
			c = a
		}
		if b > d {
			b = d
		}
		if b > c {
			b, c = c, b
		}
		out[i] = (b + c) / 2
	}
	return out
}
