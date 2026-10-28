// Ported from pele src/layout/lanes.ts at 8aff183be57021b533f319a29341a529be934b08.

package layout

import (
	"math"
	"slices"

	"tuios.dev/sketch/internal/js"
)

// Layout for swimlanes. Every top-level group is a lane: a band that runs the length of the
// diagram along the flow. Ranks are shared by all lanes, so a step sits level with the steps
// that happen at the same time in other lanes, and each node stays inside its own lane.

const (
	laneHead = 34
	// LanePad is the room a lane keeps around its items unless the caller asks for less.
	LanePad     = 24
	laneSweeps  = 8
	laneDummies = 20000
	laneHops    = 10000
	routeBudget = 2000000
	// Parallel lines closer than this are drawn as one.
	laneNear   = 4
	laneRounds = 3
	laneRelay  = 8
	laneTurn   = 20
	// The room a lane keeps beside a handoff along its margin, and how much of it is between the
	// handoff and the lane's items.
	laneMargin = 20
	laneLeg    = 14
	laneClose  = 3
)

// laneArena hands out the scratch slices of one placing of the lanes, from a few large
// blocks that the next placing reuses. Not in pele.
type laneArena struct {
	ints, intBlock     []int
	floats, floatBlock []float64
}

// reset starts handing out the blocks again, made at least this large.
func (a *laneArena) reset(ints, floats int) {
	if len(a.intBlock) < ints {
		a.intBlock = make([]int, ints)
	}
	if len(a.floatBlock) < floats {
		a.floatBlock = make([]float64, floats)
	}
	a.ints, a.floats = a.intBlock, a.floatBlock
}

func (a *laneArena) int(n int) []int {
	if n > len(a.ints) {
		a.intBlock = make([]int, max(n, 2*len(a.intBlock)))
		a.ints = a.intBlock
	}
	out := a.ints[:n:n]
	a.ints = a.ints[n:]
	clear(out)
	return out
}

func (a *laneArena) float(n int) []float64 {
	if n > len(a.floats) {
		a.floatBlock = make([]float64, max(n, 2*len(a.floatBlock)))
		a.floats = a.floatBlock
	}
	out := a.floats[:n:n]
	a.floats = a.floats[n:]
	clear(out)
	return out
}

type lanePlaced struct {
	width   float64
	height  float64
	routed  []uint8
	margins []uint8
	work    int
}

// LaneLayout lays out a graph whose top-level groups are lanes. lanePad is the room a lane
// keeps around its items; pele's default is LanePad.
func LaneLayout(nodes []*CNode, edges []*CEdge, dir Dir, opt LayeredOptions, lanePad float64) CompoundResult {
	n := len(nodes)
	if n == 0 {
		return CompoundResult{}
	}
	sideways := dir == "LR" || dir == "RL"
	flipped := dir == "BT" || dir == "RL"

	// Lanes in the order they were written. The graph lists groups last-declared first.
	var lanes []int
	for i := n - 1; i >= 0; i-- {
		if nodes[i].Parent < 0 && nodes[i].IsGroup {
			lanes = append(lanes, i)
		}
	}
	laneCount := max(1, len(lanes))
	laneSlot := fillInt(n, -1)
	for k, id := range lanes {
		laneSlot[id] = k
	}
	hasChild := make([]uint8, n)
	for i := 0; i < n; i++ {
		if nodes[i].Parent >= 0 {
			hasChild[nodes[i].Parent] = 1
		}
	}
	// Room a lane keeps around its items, so that the groups drawn around them stay inside it.
	lanePads := make([]float64, laneCount)
	for k := range lanePads {
		lanePads[k] = lanePad
	}
	endPad := lanePad
	laneOf := func(i int) int {
		t := i
		room := lanePad
		for hops := 0; nodes[t].Parent >= 0 && hops < laneHops; hops++ {
			t = nodes[t].Parent
			if laneSlot[t] < 0 {
				room += math.Max(nodes[t].PadX, math.Max(nodes[t].PadTop, nodes[t].PadBottom))
			}
		}
		lane := max(0, laneSlot[t])
		if room > lanePads[lane] {
			lanePads[lane] = room
		}
		if room > endPad {
			endPad = room
		}
		return lane
	}

	// What gets ranked and placed: nodes, and groups that are empty and so act as nodes.
	itemOf := fillInt(n, -1)
	var real []int
	for i := 0; i < n; i++ {
		if laneSlot[i] >= 0 {
			continue
		}
		if !nodes[i].IsGroup || hasChild[i] == 0 {
			itemOf[i] = len(real)
			real = append(real, i)
		}
	}
	along := func(c *CNode) float64 {
		if sideways {
			return c.W
		}
		return c.H
	}
	cross := func(c *CNode) float64 {
		if sideways {
			return c.H
		}
		return c.W
	}

	// The nodes and edges of the ranking, each held in one block.
	lnodes := make([]*LNode, len(real))
	nodeBlock := make([]LNode, len(real))
	for k, i := range real {
		nodeBlock[k] = LNode{W: cross(nodes[i]), H: along(nodes[i]), Kind: KindNode, Hint: math.NaN(), Key: math.NaN()}
		lnodes[k] = &nodeBlock[k]
	}
	ledges := make([]*LEdge, 0, len(edges))
	edgeBlock := make([]LEdge, 0, len(edges))
	edgeOf := make([]int, 0, len(edges))
	step := 1
	{
		seen := make(map[int]bool, len(edges))
		for ei, e := range edges {
			a := itemOf[e.Src]
			b := itemOf[e.Dst]
			if a < 0 || b < 0 || a == b {
				continue
			}
			key := min(a, b)*len(real) + max(a, b)
			if e.LabelW > 0 || seen[key] {
				step = 2
			}
			seen[key] = true
			le := LEdge{Tail: a, Head: b, Minlen: e.Minlen, LabelW: e.LabelW, LabelH: e.LabelH, Points: []float64{}}
			if sideways {
				le.LabelW, le.LabelH = e.LabelH, e.LabelW
			}
			edgeBlock = append(edgeBlock, le)
			ledges = append(ledges, &edgeBlock[len(edgeBlock)-1])
			edgeOf = append(edgeOf, ei)
		}
	}
	Orient(lnodes, ledges)
	Rank(lnodes, ledges, step)

	m := len(ledges)
	itemLane := make([]int, len(real))
	for k, i := range real {
		itemLane[k] = laneOf(i)
	}
	// Lays the ranked items out and routes the relations. A plain relation is a handoff that takes
	// a simple route of its own, and so keeps no room through the ranks it passes.
	var arena laneArena
	var pts, way []float64
	place := func(plain []uint8, margins []uint8) lanePlaced {
		dummies := 0
		for k := 0; k < m; k++ {
			if plain[k] == 0 {
				dummies += absInt(lnodes[ledges[k].Head].Rank-lnodes[ledges[k].Tail].Rank) - 1
			}
		}
		ranks := 1
		for _, v := range lnodes {
			ranks = max(ranks, v.Rank+1)
		}
		// Every placing takes its scratch from the blocks the first one made, sized for it.
		items := len(real) + dummies
		arena.reset(9*items+7*m+2*n+ranks+8, 7*items+4*n+3*ranks+3*laneCount+8)
		pad := arena.float(laneCount)
		for lane, room := range lanePads {
			if margins[lane] != 0 {
				pad[lane] = math.Max(room, laneMargin)
			} else {
				pad[lane] = room
			}
		}
		used := make([]uint8, laneCount)
		// Items: the real ones, then a dummy for every rank an edge passes through.
		dummies = 0
		span := arena.int(m)
		for k := 0; k < m; k++ {
			span[k] = absInt(lnodes[ledges[k].Head].Rank - lnodes[ledges[k].Tail].Rank)
			if plain[k] == 0 {
				dummies += span[k] - 1
			}
		}
		direct := dummies > laneDummies
		total := len(real)
		if !direct {
			total += dummies
		}
		RANK := arena.int(total)
		LANE := arena.int(total)
		SIZE := arena.float(total)
		DEPTH := arena.float(total)
		DUMMY := make([]uint8, total)
		maxRank := 0
		for k := 0; k < len(real); k++ {
			RANK[k] = lnodes[k].Rank
			LANE[k] = itemLane[k]
			SIZE[k] = lnodes[k].W
			DEPTH[k] = lnodes[k].H
			if RANK[k] > maxRank {
				maxRank = RANK[k]
			}
		}
		firstDummy := arena.int(m)
		labelItem := arena.int(m)
		for k := range firstDummy {
			firstDummy[k], labelItem[k] = -1, -1
		}
		links := arena.int(2 * (m + dummies))[:0]
		{
			next := len(real)
			for k := 0; k < m; k++ {
				e := ledges[k]
				a, b := e.Tail, e.Head
				if e.Reversed {
					a, b = e.Head, e.Tail
				}
				if direct || span[k] < 2 || plain[k] != 0 {
					links = append(links, a, b)
					continue
				}
				ra := RANK[a]
				rb := RANK[b]
				labelRank := -1
				if e.LabelW > 0 {
					labelRank = ra + ((rb - ra) >> 1)
					if step == 2 && (labelRank&1) == 0 {
						labelRank--
					}
				}
				firstDummy[k] = next
				prev := a
				for r := ra + 1; r < rb; r++ {
					d := next
					next++
					RANK[d] = r
					// The edge crosses from its first lane to its last one evenly along the way.
					LANE[d] = LANE[a] + int(js.Round(float64((LANE[b]-LANE[a])*(r-ra))/float64(rb-ra)))
					DUMMY[d] = 1
					if r == labelRank {
						SIZE[d] = e.LabelW
						DEPTH[d] = e.LabelH
						labelItem[k] = d
					}
					links = append(links, prev, d)
					prev = d
				}
				links = append(links, prev, b)
			}
		}

		upStart := arena.int(total + 1)
		downStart := arena.int(total + 1)
		for k := 0; k < len(links); k += 2 {
			downStart[links[k]+1]++
			upStart[links[k+1]+1]++
		}
		for i := 0; i < total; i++ {
			upStart[i+1] += upStart[i]
			downStart[i+1] += downStart[i]
		}
		up := arena.int(len(links) / 2)
		down := arena.int(len(links) / 2)
		{
			uf := arena.int(total)
			copy(uf, upStart)
			df := arena.int(total)
			copy(df, downStart)
			for k := 0; k < len(links); k += 2 {
				down[df[links[k]]] = links[k+1]
				df[links[k]]++
				up[uf[links[k+1]]] = links[k]
				uf[links[k+1]]++
			}
		}

		// Order within each rank: by lane first, then by where the item's neighbours sit.
		rows := make([][]int, maxRank+1)
		{
			// The items of each rank, in order, as slices of one block.
			count := arena.int(maxRank + 2)
			for i := 0; i < total; i++ {
				count[RANK[i]+1]++
			}
			for r := 0; r <= maxRank; r++ {
				count[r+1] += count[r]
			}
			flat := arena.int(total)
			for r := range rows {
				rows[r] = flat[count[r]:count[r]:count[r+1]]
			}
			for i := 0; i < total; i++ {
				rows[RANK[i]] = append(rows[RANK[i]], i)
			}
		}
		pos := arena.float(total)
		key := arena.float(total)
		settle := func(row []int) {
			js.Sort(row, func(a, b int) float64 {
				if d := LANE[a] - LANE[b]; d != 0 {
					return float64(d)
				}
				if d := key[a] - key[b]; d != 0 && !math.IsNaN(d) {
					return d
				}
				return float64(a - b)
			})
			for k := 0; k < len(row); k++ {
				pos[row[k]] = float64(k)
			}
		}
		for _, row := range rows {
			for k := 0; k < len(row); k++ {
				key[row[k]] = float64(k)
			}
			settle(row)
		}
		for sweep := 0; sweep < laneSweeps && maxRank > 0; sweep++ {
			downward := (sweep & 1) == 0
			for s := 1; s <= maxRank; s++ {
				row := rows[maxRank-s]
				start, list := downStart, down
				if downward {
					row = rows[s]
					start, list = upStart, up
				}
				for _, i := range row {
					sum := 0.0
					count := start[i+1] - start[i]
					for k := start[i]; k < start[i+1]; k++ {
						sum += pos[list[k]]
					}
					if count > 0 {
						key[i] = sum / float64(count)
					} else {
						key[i] = pos[i]
					}
				}
				settle(row)
			}
		}

		// The items of one rank that share a lane sit side by side as a run.
		gap := func(a, b int) float64 {
			if DUMMY[a] != 0 || DUMMY[b] != 0 {
				return opt.EdgeSep
			}
			return opt.NodeSep
		}
		eachRun := func(row []int, visit func(row []int, from, to int)) {
			from := 0
			for from < len(row) {
				to := from
				for to+1 < len(row) && LANE[row[to+1]] == LANE[row[from]] {
					to++
				}
				visit(row, from, to)
				from = to + 1
			}
		}
		runSize := func(row []int, from, to int) float64 {
			size := SIZE[row[from]]
			for k := from; k < to; k++ {
				size += gap(row[k], row[k+1]) + SIZE[row[k+1]]
			}
			return size
		}

		// Lane breadth: enough for its fullest rank, and for its title.
		breadth := arena.float(laneCount)
		head := 0.0
		if len(lanes) > 0 {
			head = laneHead
		}
		for k := 0; k < len(lanes); k++ {
			lane := nodes[lanes[k]]
			breadth[k] = math.Max(0, lane.MinW-float64(2*pad[k]))
			if lane.PadTop > head {
				head = lane.PadTop
			}
		}
		for _, row := range rows {
			eachRun(row, func(_ []int, from, to int) {
				size := runSize(row, from, to)
				if size > breadth[LANE[row[from]]] {
					breadth[LANE[row[from]]] = size
				}
			})
		}
		laneStart := arena.float(laneCount + 1)
		for k := 0; k < laneCount; k++ {
			laneStart[k+1] = laneStart[k] + breadth[k] + float64(2*pad[k])
		}

		// Across the flow: each lane's items start centered, then line up with their neighbours in
		// the same lane as far as the lane and the items beside them allow.
		C := arena.float(total)
		want := arena.float(total)
		center := func(row []int, from, to int) {
			lane := LANE[row[from]]
			at := (laneStart[lane] + pad[lane] + (laneStart[lane+1] - pad[lane]) - runSize(row, from, to)) / 2
			for k := from; k <= to; k++ {
				C[row[k]] = at + SIZE[row[k]]/2
				if k < to {
					at += SIZE[row[k]] + gap(row[k], row[k+1])
				}
			}
		}
		align := func(row []int, from, to int) {
			lane := LANE[row[from]]
			floor := laneStart[lane] + pad[lane]
			for k := from; k <= to; k++ {
				i := row[k]
				C[i] = math.Max(want[i], floor+SIZE[i]/2)
				if k < to {
					floor = C[i] + SIZE[i]/2 + gap(i, row[k+1])
				}
			}
			ceil := laneStart[lane+1] - pad[lane]
			for k := to; k >= from; k-- {
				i := row[k]
				C[i] = math.Min(C[i], ceil-SIZE[i]/2)
				if k > from {
					ceil = C[i] - SIZE[i]/2 - gap(row[k-1], i)
				}
			}
		}
		for _, row := range rows {
			eachRun(row, center)
		}
		for pass := 0; pass < 4 && maxRank > 0; pass++ {
			downward := (pass & 1) == 0
			for s := 1; s <= maxRank; s++ {
				row := rows[maxRank-s]
				start, list := downStart, down
				if downward {
					row = rows[s]
					start, list = upStart, up
				}
				for _, i := range row {
					sum := 0.0
					count := 0
					for k := start[i]; k < start[i+1]; k++ {
						if LANE[list[k]] != LANE[i] {
							continue
						}
						sum += C[list[k]]
						count++
					}
					if count > 0 {
						want[i] = sum / float64(count)
					} else {
						want[i] = C[i]
					}
				}
				eachRun(row, align)
			}
		}

		// Along the flow: one band per rank, after the lane titles.
		bandStart := arena.float(maxRank + 1)
		bandEnd := arena.float(maxRank + 1)
		// The room after a rank is for the relations that turn there or are only as long as they
		// were asked to be. Where just plain handoffs and straight lines pass, the ranks close up,
		// as far as the label of a handoff between them leaves it a line to sit on.
		full := opt.RankSep / float64(step)
		sep := arena.float(maxRank + 1)
		for r := range sep {
			sep[r] = full
		}
		if slices.Contains(plain, 1) {
			for r := range sep {
				sep[r] = full / laneClose
			}
			for k := 0; k < m; k++ {
				e := ledges[k]
				a, b := e.Tail, e.Head
				if e.Reversed {
					a, b = e.Head, e.Tail
				}
				if plain[k] != 0 {
					if e.LabelH <= 0 || span[k] > step {
						continue
					}
					room := math.Min(full, (e.LabelH+2*laneTurn-math.Min(DEPTH[a], DEPTH[b])/2)/float64(span[k]))
					for r := RANK[a]; r < RANK[b]; r++ {
						if room > sep[r] {
							sep[r] = room
						}
					}
					continue
				}
				d0 := firstDummy[k]
				if d0 < 0 || span[k] <= step*e.Minlen {
					for r := RANK[a]; r < RANK[b]; r++ {
						sep[r] = full
					}
					continue
				}
				for d := d0 - 1; d < d0+span[k]-1; d++ {
					u := d
					if d < d0 {
						u = a
					}
					v := b
					if d+1 < d0+span[k]-1 {
						v = d + 1
					}
					if LANE[u] != LANE[v] || math.Abs(C[u]-C[v]) >= 0.5 {
						sep[RANK[u]] = full
					}
				}
			}
		}
		at := head + endPad
		for r := 0; r <= maxRank; r++ {
			depth := 0.0
			for _, i := range rows[r] {
				if DEPTH[i] > depth {
					depth = DEPTH[i]
				}
			}
			bandStart[r] = at
			bandEnd[r] = at + depth
			if r == maxRank {
				at += depth
			} else {
				at += depth + sep[r]
			}
		}
		length := at + endPad
		breadthTotal := laneStart[laneCount]
		flip := func(a float64) float64 {
			if flipped {
				return head + length - a
			}
			return a
		}
		px := func(c, a float64) float64 {
			if sideways {
				return flip(a)
			}
			return c
		}
		py := func(c, a float64) float64 {
			if sideways {
				return c
			}
			return flip(a)
		}
		axis := 0.0
		if sideways {
			axis = 1
		}

		for k := 0; k < len(real); k++ {
			node := nodes[real[k]]
			a := (bandStart[RANK[k]] + bandEnd[RANK[k]]) / 2
			node.X = px(C[k], a)
			node.Y = py(C[k], a)
		}
		for k := 0; k < len(lanes); k++ {
			lane := nodes[lanes[k]]
			c := (laneStart[k] + laneStart[k+1]) / 2
			size := laneStart[k+1] - laneStart[k]
			if sideways {
				lane.X, lane.Y, lane.W, lane.H = length/2, c, length, size
			} else {
				lane.X, lane.Y, lane.W, lane.H = c, length/2, size, length
			}
			lane.PadTop = head
		}

		// Groups inside a lane are drawn around their members, deepest first.
		depthOf := arena.int(n)
		inner := arena.int(n)[:0]
		for i := 0; i < n; i++ {
			if !nodes[i].IsGroup || laneSlot[i] >= 0 || itemOf[i] >= 0 {
				continue
			}
			d := 0
			for p := nodes[i].Parent; p >= 0 && d < laneHops; p = nodes[p].Parent {
				d++
			}
			depthOf[i] = d
			inner = append(inner, i)
		}
		js.Sort(inner, func(a, b int) float64 {
			if d := depthOf[b] - depthOf[a]; d != 0 {
				return float64(d)
			}
			return float64(a - b)
		})
		box := arena.float(n * 4)
		for i := 0; i < n; i++ {
			box[i*4] = math.Inf(1)
			box[i*4+1] = math.Inf(1)
			box[i*4+2] = math.Inf(-1)
			box[i*4+3] = math.Inf(-1)
		}
		grow := func(g int, c *CNode) {
			if c.X-c.W/2 < box[g*4] {
				box[g*4] = c.X - c.W/2
			}
			if c.Y-c.H/2 < box[g*4+1] {
				box[g*4+1] = c.Y - c.H/2
			}
			if c.X+c.W/2 > box[g*4+2] {
				box[g*4+2] = c.X + c.W/2
			}
			if c.Y+c.H/2 > box[g*4+3] {
				box[g*4+3] = c.Y + c.H/2
			}
		}
		for _, i := range real {
			if nodes[i].Parent >= 0 && laneSlot[nodes[i].Parent] < 0 {
				grow(nodes[i].Parent, nodes[i])
			}
		}
		for _, g := range inner {
			c := nodes[g]
			if math.IsInf(box[g*4], 1) {
				continue
			}
			w := math.Max(box[g*4+2]-box[g*4]+float64(2*c.PadX), c.MinW)
			c.X = (box[g*4] + box[g*4+2]) / 2
			c.W = w
			c.Y = (box[g*4+1] - c.PadTop + box[g*4+3] + c.PadBottom) / 2
			c.H = box[g*4+3] - box[g*4+1] + c.PadTop + c.PadBottom
			if c.Parent >= 0 && laneSlot[c.Parent] < 0 {
				grow(c.Parent, c)
			}
		}

		for _, e := range edges {
			e.Route = []float64{}
		}
		// The routes of a placing are slices of one block: at most two points a rank.
		routeRoom := 0
		for k := 0; k < m; k++ {
			routeRoom += 3 * (2 + 2*max(span[k]-1, 0))
		}
		block := make([]float64, 0, routeRoom)
		for k := 0; k < m; k++ {
			e := ledges[k]
			out := edges[edgeOf[k]]
			a, b := e.Tail, e.Head
			if e.Reversed {
				a, b = e.Head, e.Tail
			}
			pts = append(pts[:0], C[a], bandEnd[RANK[a]]-(bandEnd[RANK[a]]-bandStart[RANK[a]]-DEPTH[a])/2)
			d0 := firstDummy[k]
			if d0 >= 0 {
				for d := d0; d < d0+span[k]-1; d++ {
					pts = append(pts, C[d], bandStart[RANK[d]])
					if bandEnd[RANK[d]] > bandStart[RANK[d]] {
						pts = append(pts, C[d], bandEnd[RANK[d]])
					}
				}
			}
			pts = append(pts, C[b], bandStart[RANK[b]]+float64((bandEnd[RANK[b]]-bandStart[RANK[b]]-DEPTH[b])/2))
			from := len(block)
			for p := 0; p < len(pts); p += 2 {
				block = append(block, px(pts[p], pts[p+1]), py(pts[p], pts[p+1]), axis)
			}
			route := block[from:len(block):len(block)]
			if e.Reversed {
				for i, j := 0, len(route)-3; i < j; i, j = i+3, j-3 {
					for t := 0; t < 2; t++ {
						route[i+t], route[j+t] = route[j+t], route[i+t]
					}
				}
			}
			out.Route = route
			label := labelItem[k]
			if label >= 0 {
				mid := (bandStart[RANK[label]] + bandEnd[RANK[label]]) / 2
				out.LabelX = px(C[label], mid)
				out.LabelY = py(C[label], mid)
			} else {
				out.LabelX = (route[0] + route[len(route)-3]) / 2
				out.LabelY = (route[1] + route[len(route)-2]) / 2
			}
		}

		// An edge that starts or ends at a lane or a group runs straight to its border.
		for _, e := range edges {
			if len(e.Route) > 0 || e.Src == e.Dst {
				continue
			}
			a := nodes[e.Src]
			b := nodes[e.Dst]
			ax, ay := laneBorder(a, b)
			bx, by := laneBorder(b, a)
			flat := 0.0
			if math.Abs(bx-ax) > math.Abs(by-ay) {
				flat = 1
			}
			e.Route = []float64{ax, ay, flat, bx, by, flat}
			e.LabelX = (ax + bx) / 2
			e.LabelY = (ay + by) / 2
		}

		// A handoff can leave towards the other lane, then arrive along the flow (or vice versa).
		// In particular, a decision's return branch must not leave through its incoming side and
		// retrace the path past the preceding step. Try the two single-corner routes, keeping the
		// ranked route when a node or another label blocks them.
		routed := make([]uint8, m)
		routePoints := 0
		for _, edge := range edges {
			routePoints += len(edge.Route) / 3
		}
		work := m * (len(real) + len(edges) + routePoints)
		if work <= routeBudget {
			amid := func(i int) float64 { return (bandStart[RANK[i]] + bandEnd[RANK[i]]) / 2 }
			labels := make([][2]float64, len(edges))
			for i, e := range edges {
				if sideways {
					labels[i] = [2]float64{e.LabelY, flip(e.LabelX)}
				} else {
					labels[i] = [2]float64{e.LabelX, flip(e.LabelY)}
				}
			}
			overlaps := func(c0, a0, c1, a1, lc, la, cw, ah float64) bool {
				return c1 > lc-cw/2 && c0 < lc+cw/2 && a1 > la-ah/2 && a0 < la+ah/2
			}
			clear := func(c0, a0, c1, a1 float64, src, dst, ei int) bool {
				for i := 0; i < len(real); i++ {
					if i != src && i != dst && overlaps(c0, a0, c1, a1, C[i], amid(i), SIZE[i]+8, DEPTH[i]+8) {
						return false
					}
				}
				for i, e := range edges {
					// A step's own loop keeps its label inside the step's box, and has no place here.
					if i == ei || e.LabelW <= 0 || e.Src == e.Dst {
						continue
					}
					cw, ah := e.LabelW, e.LabelH
					if sideways {
						cw, ah = e.LabelH, e.LabelW
					}
					if overlaps(c0, a0, c1, a1, labels[i][0], labels[i][1], cw, ah) {
						return false
					}
				}
				return true
			}
			// How many other relations have a stretch inside a box. A stretch's whole box counts,
			// including its curve.
			crossings := func(x0, y0, x1, y1 float64, ei int) int {
				count := 0
				for i := range edges {
					if i == ei {
						continue
					}
					route := edges[i].Route
					for j := 3; j < len(route); j += 3 {
						if math.Max(route[j-3], route[j]) > x0 && math.Min(route[j-3], route[j]) < x1 &&
							math.Max(route[j-2], route[j+1]) > y0 && math.Min(route[j-2], route[j+1]) < y1 {
							count++
							break
						}
					}
				}
				return count
			}
			meets := func(x, y float64, ei int) int {
				count := 0
				for i := range edges {
					route := edges[i].Route
					if i == ei || len(route) == 0 {
						continue
					}
					last := len(route) - 3
					if (math.Abs(route[0]-x) < 1 && math.Abs(route[1]-y) < 1) || (math.Abs(route[last]-x) < 1 && math.Abs(route[last+1]-y) < 1) {
						count++
					}
				}
				return count
			}
			// Two relations drawn along one line read as a single path. Those that leave the same step
			// or arrive at the same step may share a trunk; any other pair must not.
			retraces := func(x0, y0, x1, y1 float64, ei int) bool {
				upright := math.Abs(x1-x0) < 0.5
				lo, hi, line := math.Min(x0, x1), math.Max(x0, x1), y0
				iu, iv := 1, 0
				if upright {
					lo, hi, line = math.Min(y0, y1), math.Max(y0, y1), x0
					iu, iv = 0, 1
				}
				for i, other := range edges {
					if i == ei || other.Src == edges[ei].Src || other.Dst == edges[ei].Dst {
						continue
					}
					route := other.Route
					for j := 3; j < len(route); j += 3 {
						u0, u1 := route[j-3+iu], route[j+iu]
						if math.Abs(u0-line) >= laneNear || math.Abs(u1-line) >= laneNear {
							continue
						}
						v0, v1 := route[j-3+iv], route[j+iv]
						if math.Min(hi, math.Max(v0, v1))-math.Max(lo, math.Min(v0, v1)) > 1 {
							return true
						}
					}
				}
				return false
			}
			for k := 0; k < m; k++ {
				e, ei := ledges[k], edgeOf[k]
				out := edges[ei]
				a, b := e.Tail, e.Head
				if LANE[a] == LANE[b] || math.Abs(C[b]-C[a]) < 1 {
					continue
				}
				ac, bc, aa, ba := C[a], C[b], amid(a), amid(b)
				dc, da := js.Sign(bc-ac), js.Sign(ba-aa)
				attempt := func(pts []float64, margin float64) bool {
					for p := 2; p < len(pts); p += 2 {
						if !clear(math.Min(pts[p-2], pts[p])-margin, math.Min(pts[p-1], pts[p+1])-margin,
							math.Max(pts[p-2], pts[p])+margin, math.Max(pts[p-1], pts[p+1])+margin, a, b, ei) {
							return false
						}
						if retraces(px(pts[p-2], pts[p-1]), py(pts[p-2], pts[p-1]), px(pts[p], pts[p+1]), py(pts[p], pts[p+1]), ei) {
							return false
						}
					}
					lc, la, longest := 0.0, 0.0, -1.0
					if out.LabelW > 0 {
						cw, ah := e.LabelW, e.LabelH
						for p := 2; p < len(pts); p += 2 {
							cc, ca := (pts[p-2]+pts[p])/2, (pts[p-1]+pts[p+1])/2
							across, along := math.Abs(pts[p]-pts[p-2]), math.Abs(pts[p+1]-pts[p-1])
							run := across + along
							short := along < ah+8
							if across > 0 {
								short = across < cw+8
							}
							if run <= longest || short {
								continue
							}
							if cc-cw/2 < 0 || cc+cw/2 > breadthTotal || ca-ah/2 < 0 || ca+ah/2 > length {
								continue
							}
							// Include the ends here: the label must clear both shapes it connects.
							if !clear(cc-cw/2, ca-ah/2, cc+cw/2, ca+ah/2, -1, -1, ei) {
								continue
							}
							// A label moved onto a handoff must not cover a return path that keeps its
							// ranked route. A segment's whole box is kept clear, including its curve.
							lx, ly := px(cc, ca), py(cc, ca)
							if crossings(lx-out.LabelW/2, ly-out.LabelH/2, lx+out.LabelW/2, ly+out.LabelH/2, ei) > 0 {
								continue
							}
							lc, la, longest = cc, ca, run
						}
						if longest < 0 {
							return false
						}
					}
					// A point short of each corner and one past it keep the turn close to the corner, so
					// that the line stays under its label and the handoff takes little room.
					way = append(way[:0], pts[0], pts[1])
					for p := 2; p < len(pts); p += 2 {
						dx, dy := pts[p]-pts[p-2], pts[p+1]-pts[p-1]
						run := math.Abs(dx) + math.Abs(dy)
						first, last := 0.0, 0.0
						if p > 2 {
							first = laneTurn
						}
						if p+2 < len(pts) {
							last = laneTurn
						}
						if run > first+last+1 {
							if first != 0 {
								way = append(way, pts[p-2]+float64((dx/run)*first), pts[p-1]+float64((dy/run)*first))
							}
							if last != 0 {
								way = append(way, pts[p]-float64((dx/run)*last), pts[p+1]-float64((dy/run)*last))
							}
						}
						way = append(way, pts[p], pts[p+1])
					}
					route := make([]float64, 0, len(way)/2*3)
					for p := 0; p < len(way); p += 2 {
						var crosswise bool
						if p == 0 {
							crosswise = way[0] != way[2]
						} else {
							crosswise = way[p] != way[p-2]
						}
						turn := axis
						if crosswise {
							turn = 1 - axis
						}
						route = append(route, px(way[p], way[p+1]), py(way[p], way[p+1]), turn)
					}
					out.Route = route
					if out.LabelW > 0 {
						out.LabelX, out.LabelY = px(lc, la), py(lc, la)
						labels[ei] = [2]float64{lc, la}
					}
					routed[k] = 1
					return true
				}
				if attempt([]float64{ac + float64(dc*SIZE[a]/2), aa, bc, aa, bc, ba - float64(da*DEPTH[b]/2)}, 2) {
					continue
				}
				if attempt([]float64{ac, aa + float64(da*DEPTH[a]/2), ac, ba, bc - float64(dc*SIZE[b]/2), ba}, 2) {
					continue
				}
				// With both corners blocked, as for a return past the steps between, the handoff goes
				// around: out to the margin of a lane, along it, and back in level with its end. It
				// takes the margin that crosses the fewest other relations and shares the fewest ends
				// with them, then the shortest way.
				if work > routeBudget/laneRelay {
					continue
				}
				type aroundWay struct {
					pts  []float64
					cost float64
					lane int
				}
				var around []aroundWay
				order := []int{LANE[a], LANE[b]}
				if LANE[a] < LANE[b] {
					order = []int{LANE[b], LANE[a]}
				}
				for _, lane := range order {
					// A lane that keeps no margin yet has less room there, and the handoff stays inside it.
					inset := math.Max(pad[lane]-laneLeg, math.Min(pad[lane]/2, laneMargin-laneLeg))
					for _, ch := range []float64{laneStart[lane] + inset, laneStart[lane+1] - inset} {
						if math.Abs(ch-ac) < SIZE[a]/2+4 || math.Abs(ch-bc) < SIZE[b]/2+4 {
							continue
						}
						pts := []float64{ac + float64(js.Sign(ch-ac)*SIZE[a]/2), aa, ch, aa, ch, ba, bc + float64(js.Sign(ch-bc)*SIZE[b]/2), ba}
						cost := float64(float64(meets(px(pts[0], pts[1]), py(pts[0], pts[1]), ei)+meets(px(pts[6], pts[7]), py(pts[6], pts[7]), ei)) * 1e6)
						for p := 2; p < len(pts); p += 2 {
							x0, y0, x1, y1 := px(pts[p-2], pts[p-1]), py(pts[p-2], pts[p-1]), px(pts[p], pts[p+1]), py(pts[p], pts[p+1])
							cost += float64(float64(crossings(math.Min(x0, x1), math.Min(y0, y1), math.Max(x0, x1), math.Max(y0, y1), ei))*1e6) + math.Abs(x1-x0) + math.Abs(y1-y0)
						}
						around = append(around, aroundWay{pts, cost, lane})
					}
				}
				js.Sort(around, func(p, q aroundWay) float64 { return p.cost - q.cost })
				for _, way := range around {
					if !attempt(way.pts, 1) {
						continue
					}
					used[way.lane] = 1
					break
				}
			}
		}

		width, height := breadthTotal, length
		if sideways {
			width, height = length, breadthTotal
		}
		return lanePlaced{width: width, height: height, routed: routed, margins: used, work: work}
	}

	plain := make([]uint8, m)
	margins := make([]uint8, laneCount)
	placed := place(plain, margins)
	// A handoff that was given a simple route needs no room kept for it through the ranks it
	// passes, so the lanes are laid out again without it. A handoff that then loses its route
	// goes back to the ranked one. Routing is most of the work, so a large graph is laid out once.
	for round := 0; round < laneRounds && placed.work <= routeBudget/laneRelay; round++ {
		next := placed.routed
		if round != 0 {
			next = make([]uint8, m)
			for k, on := range plain {
				next[k] = on & placed.routed[k]
			}
		}
		if slices.Equal(next, plain) && slices.Equal(placed.margins, margins) {
			break
		}
		plain = next
		margins = placed.margins
		placed = place(plain, margins)
	}
	for k, on := range plain {
		if on != 0 && placed.routed[k] == 0 {
			placed = place(make([]uint8, m), make([]uint8, laneCount))
			break
		}
	}
	return CompoundResult{Width: placed.width, Height: placed.height}
}

// laneBorder is the point where the line from one box's center to another's leaves the first box. When the
// other center is inside the box, the point is on the nearest side instead.
func laneBorder(from, to *CNode) (float64, float64) {
	dx := to.X - from.X
	dy := to.Y - from.Y
	hw := math.Max(from.W/2, 0.5)
	hh := math.Max(from.H/2, 0.5)
	if math.Abs(dx) < hw && math.Abs(dy) < hh {
		toSide := hw - math.Abs(dx)
		toEnd := hh - math.Abs(dy)
		if toSide < toEnd {
			if dx < 0 {
				return from.X - hw, to.Y
			}
			return from.X + hw, to.Y
		}
		if dy < 0 {
			return to.X, from.Y - hh
		}
		return to.X, from.Y + hh
	}
	sx, sy := math.Inf(1), math.Inf(1)
	if dx != 0 {
		sx = hw / math.Abs(dx)
	}
	if dy != 0 {
		sy = hh / math.Abs(dy)
	}
	scale := math.Min(sx, sy)
	return from.X + float64(dx*scale), from.Y + float64(dy*scale)
}
