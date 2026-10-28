// Ported from pele src/diagrams/architecture/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package architecture

import (
	"math"
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

const (
	STUB      = 12
	CORNER    = 6
	LABEL_GAP = 4
	DOT       = 3
)

func setting(config types.Config, key string, fallback, lo, hi float64) float64 {
	if section, ok := config["architecture"].(map[string]any); ok {
		if value, ok := section[key].(float64); ok && !math.IsNaN(value) && !math.IsInf(value, 0) {
			return math.Min(hi, math.Max(lo, value))
		}
	}
	return fallback
}

// rank maps the grid lines in use to consecutive indexes, so empty rows and columns take no room.
func rank(values []int) map[int]int {
	slices.Sort(values)
	out := make(map[int]int, len(values))
	for _, value := range values {
		if _, ok := out[value]; !ok {
			out[value] = len(out)
		}
	}
	return out
}

func iconSlot(b *strings.Builder, name string, x, y, side float64, icons text.IconResolver) {
	svg.WriteStrings(b, `<svg class="pele-icon" data-icon="`, svg.EscText(name), `" x="`)
	svg.WriteNum(b, x)
	b.WriteString(`" y="`)
	svg.WriteNum(b, y)
	b.WriteString(`" width="`)
	svg.WriteNum(b, side)
	b.WriteString(`" height="`)
	svg.WriteNum(b, side)
	b.WriteString(`" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">`)
	inner := ""
	if icons != nil {
		inner = icons(name)
	}
	if inner == "" {
		inner = builtinIcon(name)
	}
	svg.WriteStrings(b, inner, `</svg>`)
}

// prefixEnd is the byte length of text.slice(0, units) for measuring: a cut inside a
// surrogate pair keeps the whole character, which pele measures the same as its high half.
func prefixEnd(s string, units int) int {
	n := 0
	for i, r := range s {
		if n >= units {
			return i
		}
		n += utf16.RuneLen(r)
	}
	return len(s)
}

// fit cuts a label down to a box, ending what is left with an ellipsis.
func fit(label text.Label, maxWidth float64, maxLines int, measurer text.TextMeasurer) text.Label {
	cut := len(label.Lines) > maxLines
	if !cut && label.Width <= maxWidth {
		return label
	}
	dots := measurer.Width("…", label.Size, 0)
	var lines [][]text.Span
	var widths []float64
	widest := 0.0
	for i := 0; i < min(maxLines, len(label.Lines)); i++ {
		line := append([]text.Span{}, label.Lines[i]...)
		width := label.Widths[i]
		if width > maxWidth || (cut && i == maxLines-1) {
			for {
				if len(line) == 0 {
					line = append(line, text.Span{Text: "…", Style: 0, Width: dots})
					width = dots
					break
				}
				span := &line[len(line)-1]
				rest := width - span.Width
				if span.Icon == "" && rest+dots < maxWidth {
					lo := 0
					hi := js.Len(span.Text)
					for lo < hi {
						mid := (lo + hi + 1) >> 1
						if rest+measurer.Width(span.Text[:prefixEnd(span.Text, mid)], label.Size, span.Style)+dots <= maxWidth {
							lo = mid
						} else {
							hi = mid - 1
						}
					}
					// A cut inside a surrogate pair steps back before the pair.
					end := prefixEnd(span.Text, lo)
					if end > 0 && unitsTo(span.Text, end) > lo {
						_, size := utf8.DecodeLastRuneInString(span.Text[:end])
						end -= size
					}
					span.Text = js.TrimEnd(span.Text[:end]) + "…"
					span.Width = measurer.Width(span.Text, label.Size, span.Style)
					width = rest + span.Width
					break
				}
				line = line[:len(line)-1]
				width = rest
			}
		}
		lines = append(lines, line)
		widths = append(widths, width)
		widest = math.Max(widest, width)
	}
	out := label
	out.Lines = lines
	out.Widths = widths
	out.Width = widest
	out.Height = float64(len(lines)) * label.LineHeight
	return out
}

// unitsTo is the UTF-16 length of s[:end].
func unitsTo(s string, end int) int {
	return js.Len(s[:end])
}

type Port struct {
	x  float64
	y  float64
	dx float64
	dy float64
	// The box the route has to stay out of when it cannot leave straight.
	x0 float64
	y0 float64
	x1 float64
	y1 float64
	// How far short of the port an arrowhead stops: the radius of a junction's dot.
	gap float64
	// Set where a junction is reached through the border of its group: the line carries on to the junction.
	wire bool
}

// route is an orthogonal route from one port to another, each left along its side's normal.
func route(s, e *Port) []float64 {
	sx, sy, sdx, sdy := s.x, s.y, s.dx, s.dy
	ex, ey, edx, edy := e.x, e.y, e.dx, e.dy
	s1x := sx + float64(sdx*STUB)
	s1y := sy + float64(sdy*STUB)
	e1x := ex + float64(edx*STUB)
	e1y := ey + float64(edy*STUB)
	if float64(sdx*edx)+float64(sdy*edy) == 0 {
		cx, cy := sx, ey
		if sdx != 0 {
			cx, cy = ex, sy
		}
		if float64((cx-sx)*sdx)+float64((cy-sy)*sdy) > 0 && float64((cx-ex)*edx)+float64((cy-ey)*edy) > 0 {
			return []float64{sx, sy, cx, cy, ex, ey}
		}
		if sdx != 0 {
			return []float64{sx, sy, s1x, s1y, s1x, e1y, e1x, e1y, ex, ey}
		}
		return []float64{sx, sy, s1x, s1y, e1x, s1y, e1x, e1y, ex, ey}
	}
	sideways := sdx != 0
	offset := ex - sx
	if sideways {
		offset = ey - sy
	}
	if sdx == -edx && sdy == -edy {
		ahead := float64((ex-sx)*sdx) + float64((ey-sy)*sdy)
		if ahead > 0 {
			if math.Abs(offset) < 0.5 {
				return []float64{sx, sy, ex, ey}
			}
			// Turn midway between the two boxes, which is clear of both titles, when there is such a place.
			if sideways {
				lo, hi := e.x1, s.x0
				if sdx > 0 {
					lo, hi = s.x1, e.x0
				}
				mx := (sx + ex) / 2
				if lo < hi {
					mx = (lo + hi) / 2
				}
				return []float64{sx, sy, mx, sy, mx, ey, ex, ey}
			}
			lo, hi := e.y1, s.y0
			if sdy > 0 {
				lo, hi = s.y1, e.y0
			}
			my := (sy + ey) / 2
			if lo < hi {
				my = (lo + hi) / 2
			}
			return []float64{sx, sy, sx, my, ex, my, ex, ey}
		}
	} else if math.Abs(offset) >= 1 {
		if sideways {
			x := math.Min(sx, ex) - STUB
			if sdx > 0 {
				x = math.Max(sx, ex) + STUB
			}
			return []float64{sx, sy, x, sy, x, ey, ex, ey}
		}
		y := math.Min(sy, ey) - STUB
		if sdy > 0 {
			y = math.Max(sy, ey) + STUB
		}
		return []float64{sx, sy, sx, y, ex, y, ex, ey}
	}
	// The straight way is blocked by one of the two nodes: go between them, or around both.
	if sideways {
		var lane float64
		switch {
		case s.y1+STUB <= e.y0:
			lane = (s.y1 + e.y0) / 2
		case e.y1+STUB <= s.y0:
			lane = (e.y1 + s.y0) / 2
		default:
			lane = math.Max(s.y1, e.y1) + STUB
		}
		return []float64{sx, sy, s1x, sy, s1x, lane, e1x, lane, e1x, ey, ex, ey}
	}
	var lane float64
	switch {
	case s.x1+STUB <= e.x0:
		lane = (s.x1 + e.x0) / 2
	case e.x1+STUB <= s.x0:
		lane = (e.x1 + s.x0) / 2
	default:
		lane = math.Max(s.x1, e.x1) + STUB
	}
	return []float64{sx, sy, sx, s1y, lane, s1y, lane, e1y, ex, e1y, ex, ey}
}

// simplify drops repeated points and joins runs that continue in a straight line.
func simplify(p []float64) []float64 {
	out := make([]float64, 2, len(p))
	out[0], out[1] = p[0], p[1]
	for i := 2; i < len(p); i += 2 {
		x := p[i]
		y := p[i+1]
		n := len(out)
		sameX := math.Abs(x-out[n-2]) < 0.01
		sameY := math.Abs(y-out[n-1]) < 0.01
		if sameX && sameY {
			continue
		}
		if n >= 4 && ((sameX && math.Abs(out[n-4]-out[n-2]) < 0.01) || (sameY && math.Abs(out[n-3]-out[n-1]) < 0.01)) {
			out[n-2] = x
			out[n-1] = y
		} else {
			out = append(out, x, y)
		}
	}
	return out
}

func pathData(b *strings.Builder, p []float64, startTrim, endTrim float64) {
	n := len(p) / 2
	xs := make([]float64, n)
	ys := make([]float64, n)
	for i := 0; i < n; i++ {
		xs[i] = p[i*2]
		ys[i] = p[i*2+1]
	}
	trim := func(at, toward int, amount float64) {
		l := js.Hypot(xs[toward]-xs[at], ys[toward]-ys[at])
		t := math.Min(amount, math.Max(0, l-1))
		if l > 0 && t > 0 {
			xs[at] += float64(((xs[toward] - xs[at]) / l) * t)
			ys[at] += float64(((ys[toward] - ys[at]) / l) * t)
		}
	}
	trim(0, 1, startTrim)
	trim(n-1, n-2, endTrim)
	b.WriteByte('M')
	svg.WriteNum(b, xs[0])
	b.WriteByte(',')
	svg.WriteNum(b, ys[0])
	for i := 1; i < n-1; i++ {
		before := js.Hypot(xs[i]-xs[i-1], ys[i]-ys[i-1])
		after := js.Hypot(xs[i+1]-xs[i], ys[i+1]-ys[i])
		r := math.Min(math.Min(CORNER, before/2), after/2)
		if r < 0.5 {
			b.WriteByte('L')
			svg.WriteNum(b, xs[i])
			b.WriteByte(',')
			svg.WriteNum(b, ys[i])
			continue
		}
		inX := xs[i] - float64(((xs[i]-xs[i-1])/before)*r)
		inY := ys[i] - float64(((ys[i]-ys[i-1])/before)*r)
		outX := xs[i] + float64(((xs[i+1]-xs[i])/after)*r)
		outY := ys[i] + float64(((ys[i+1]-ys[i])/after)*r)
		b.WriteByte('L')
		svg.WriteNum(b, inX)
		b.WriteByte(',')
		svg.WriteNum(b, inY)
		b.WriteByte('Q')
		svg.WriteNum(b, xs[i])
		b.WriteByte(',')
		svg.WriteNum(b, ys[i])
		b.WriteByte(' ')
		svg.WriteNum(b, outX)
		b.WriteByte(',')
		svg.WriteNum(b, outY)
	}
	b.WriteByte('L')
	svg.WriteNum(b, xs[n-1])
	b.WriteByte(',')
	svg.WriteNum(b, ys[n-1])
}

func renderArchitecture(model *ArchitectureDb, config types.Config, options types.RenderOptions) types.Rendered {
	return common.Tighten(options, func(tight bool) types.Rendered { return draw(model, config, options, tight) })
}

func draw(model *ArchitectureDb, config types.Config, options types.RenderOptions, tight bool) types.Rendered {
	iconSize := setting(config, "iconSize", 48, 8, 512)
	size := setting(config, "fontSize", types.Or(options.FontSize, 16), 4, 128)
	groupPad := setting(config, "padding", 20, 0, 256)
	if tight {
		groupPad = math.Min(groupPad, 12)
	}
	small := js.Round(size * 0.875)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)
	icons := options.Icons
	half := iconSize / 2

	nodes := model.nodes
	groups := model.groups
	N := len(nodes)
	G := len(groups)
	grid := placeOnGrid(nodes, groups, model.edges, model.layoutHints)
	index := make(map[string]int, N)
	for i, node := range nodes {
		index[node.id] = i
	}

	// A service is its icon square with the title beneath; a junction takes the same room.
	labels := make([]text.Label, N)
	boxW := make([]float64, N)
	boxH := make([]float64, N)
	wrap := math.Max(iconSize*2.25, 96)
	if tight {
		wrap = math.Max(iconSize, 64)
	}
	for i, node := range nodes {
		title := ""
		if node.typ == "service" {
			title = node.title
		}
		label := text.LayoutLabel(title, true, measurer, size, wrap, 0)
		labels[i] = label
		boxW[i] = math.Max(iconSize, 2*math.Ceil(label.Width/2))
		boxH[i] = iconSize
		if label.Height > 0 {
			boxH[i] += LABEL_GAP + label.Height
		}
	}

	colValues := make([]int, 0, N)
	rowValues := make([]int, 0, N)
	for i := 0; i < N; i++ {
		colValues = append(colValues, grid.col[i])
		rowValues = append(rowValues, grid.row[i])
	}
	for g := 0; g < G; g++ {
		if !grid.groupEmpty[g] {
			continue
		}
		colValues = append(colValues, grid.groupCol[g])
		rowValues = append(rowValues, grid.groupRow[g])
	}
	colOf := rank(colValues)
	rowOf := rank(rowValues)
	C := len(colOf)
	R := len(rowOf)
	colW := make([]float64, C)
	rowH := make([]float64, R)
	gap := math.Max(40, js.Round(iconSize*0.75))
	if tight {
		gap = math.Max(24, 2*STUB)
	}
	gapX := make([]float64, C)
	for c := range gapX {
		gapX[c] = gap
	}
	gapY := make([]float64, R)
	for r := range gapY {
		gapY[r] = gap
	}
	nodeCol := make([]int, N)
	nodeRow := make([]int, N)
	for i := 0; i < N; i++ {
		c := colOf[grid.col[i]]
		r := rowOf[grid.row[i]]
		nodeCol[i] = c
		nodeRow[i] = r
		colW[c] = math.Max(colW[c], boxW[i])
		rowH[r] = math.Max(rowH[r], boxH[i])
	}
	for g := 0; g < G; g++ {
		if !grid.groupEmpty[g] {
			continue
		}
		c := colOf[grid.groupCol[g]]
		r := rowOf[grid.groupRow[g]]
		colW[c] = math.Max(colW[c], iconSize)
		rowH[r] = math.Max(rowH[r], half)
	}

	// An edge label needs the gap its edge crosses to be wide enough to hold it.
	E := len(model.edges)
	edgeLabels := make([]text.Label, E)
	ends := make([]int, E*2)
	for k, edge := range model.edges {
		a, ok := index[edge.lhsId]
		if !ok {
			a = -1
		}
		b, ok := index[edge.rhsId]
		if !ok {
			b = -1
		}
		ends[k*2] = a
		ends[k*2+1] = b
		label := text.LayoutLabel(edge.title, true, measurer, small, 160, 0)
		edgeLabels[k] = label
		if label.Width == 0 || a < 0 || b < 0 {
			continue
		}
		if nodeCol[a] != nodeCol[b] {
			c := min(nodeCol[a], nodeCol[b])
			gapX[c] = math.Max(gapX[c], math.Ceil(label.Width)+28)
		} else {
			colW[nodeCol[a]] = math.Max(colW[nodeCol[a]], float64(2*math.Ceil(label.Width/2))+8)
			if nodeRow[a] != nodeRow[b] {
				r := min(nodeRow[a], nodeRow[b])
				gapY[r] = math.Max(gapY[r], math.Ceil(label.Height)+24)
			}
		}
	}

	// Room around each group, kept on the rows and columns at its edges. A group that shares an
	// edge with a group inside it stands that much further out. Children come after their parents,
	// so walking backwards settles every group before the one that holds it.
	groupLabels := make([]text.Label, G)
	c0 := make([]int, G)
	c1 := make([]int, G)
	r0 := make([]int, G)
	r1 := make([]int, G)
	insL := make([]float64, G)
	insR := make([]float64, G)
	insT := make([]float64, G)
	insB := make([]float64, G)
	headW := make([]float64, G)
	headH := make([]float64, G)
	colL := make([]float64, C)
	colR := make([]float64, C)
	rowT := make([]float64, R)
	rowB := make([]float64, R)
	badge := js.Round(size * 1.25)
	for g := 0; g < G; g++ {
		c0[g] = colOf[grid.groupCol[g]]
		c1[g] = colOf[grid.groupCol[g]+grid.groupCols[g]-1]
		r0[g] = rowOf[grid.groupRow[g]]
		r1[g] = rowOf[grid.groupRow[g]+grid.groupRows[g]-1]
		room := 320.0
		if tight {
			room = math.Max(80, common.TitleRoom(options)/2)
		}
		label := text.LayoutLabel(groups[g].title, true, measurer, size, room, 0)
		groupLabels[g] = label
		icon := 0.0
		if groups[g].icon != "" {
			icon = badge
		}
		headW[g] = label.Width + icon
		if label.Width > 0 && icon > 0 {
			headW[g] += 6
		}
		headH[g] = math.Max(label.Height, icon)
	}
	for g := G - 1; g >= 0; g-- {
		left := groupPad + insL[g]
		right := groupPad + insR[g]
		top := groupPad + insT[g]
		if headH[g] > 0 {
			top = math.Max(groupPad, headH[g]+12) + insT[g]
		}
		bottom := groupPad + insB[g]
		if headW[g] > 0 {
			inner := left + right
			for c := c0[g]; c <= c1[g]; c++ {
				inner += colW[c]
				if c < c1[g] {
					inner += colR[c] + gapX[c] + colL[c+1]
				}
			}
			if headW[g]+20 > inner {
				right += math.Ceil(headW[g] + 20 - inner)
			}
		}
		insL[g] = left
		insR[g] = right
		insT[g] = top
		insB[g] = bottom
		colL[c0[g]] = math.Max(colL[c0[g]], left)
		colR[c1[g]] = math.Max(colR[c1[g]], right)
		rowT[r0[g]] = math.Max(rowT[r0[g]], top)
		rowB[r1[g]] = math.Max(rowB[r1[g]], bottom)
		p := grid.groupParent[g]
		if p < 0 {
			continue
		}
		if c0[p] == c0[g] {
			insL[p] = math.Max(insL[p], left)
		}
		if c1[p] == c1[g] {
			insR[p] = math.Max(insR[p], right)
		}
		if r0[p] == r0[g] {
			insT[p] = math.Max(insT[p], top)
		}
		if r1[p] == r1[g] {
			insB[p] = math.Max(insB[p], bottom)
		}
	}

	X := make([]float64, C)
	Y := make([]float64, R)
	innerW := 0.0
	for c := 0; c < C; c++ {
		X[c] = innerW + colL[c]
		innerW = X[c] + colW[c] + colR[c]
		if c < C-1 {
			innerW += gapX[c]
		}
	}
	innerH := 0.0
	for r := 0; r < R; r++ {
		Y[r] = innerH + rowT[r]
		innerH = Y[r] + rowH[r] + rowB[r]
		if r < R-1 {
			innerH += gapY[r]
		}
	}
	// What is drawn, which an edge that has to go around a node can push past the grid.
	minX := 0.0
	minY := 0.0
	maxX := innerW
	maxY := innerH

	left := func(g int) float64 { return X[c0[g]] - insL[g] }
	right := func(g int) float64 { return X[c1[g]] + colW[c1[g]] + insR[g] }
	top := func(g int) float64 { return Y[r0[g]] - insT[g] }
	bottom := func(g int) float64 { return Y[r1[g]] + rowH[r1[g]] + insB[g] }
	centerX := func(i int) float64 { return X[nodeCol[i]] + colW[nodeCol[i]]/2 }

	port := func(i, side int, viaGroup bool) Port {
		dx := float64(DX[side])
		dy := float64(DY[side])
		cx := centerX(i)
		y := Y[nodeRow[i]]
		p := Port{x: cx, y: y + half, dx: dx, dy: dy, x0: cx - boxW[i]/2, y0: y, x1: cx + boxW[i]/2, y1: y + boxH[i]}
		if nodes[i].typ == "junction" {
			p.x0, p.x1 = cx, cx
			p.y0, p.y1 = p.y, p.y
			p.gap = DOT
		} else if dx != 0 {
			p.x = cx + float64(dx*half)
		} else if dy < 0 {
			p.y = y
		} else {
			p.y = y + boxH[i]
			if labels[i].Height > 0 {
				p.y += 2
			}
		}
		g := -1
		if viaGroup {
			g = grid.nodeParent[i]
		}
		if g >= 0 {
			p.x0 = left(g)
			p.x1 = right(g)
			p.y0 = top(g)
			p.y1 = bottom(g)
			p.gap = 0
			p.wire = nodes[i].typ == "junction"
			if dx != 0 {
				if dx < 0 {
					p.x = p.x0
				} else {
					p.x = p.x1
				}
			} else if dy < 0 {
				p.y = p.y0
			} else {
				p.y = p.y1
			}
		}
		return p
	}

	var geo *types.Geometry
	if options.Geometry != nil {
		geo = &types.Geometry{}
	}
	var edgesOut, labelsOut strings.Builder
	// Vertical runs of the edges, to keep group titles out from under them.
	var runs []float64
	trimLength := svg.MarkerTrim("arrow_point")
	for k, edge := range model.edges {
		a := ends[k*2]
		b := ends[k*2+1]
		if a < 0 || b < 0 {
			continue
		}
		s := port(a, sideIndex(edge.lhsDir), edge.lhsGroup)
		e := port(b, sideIndex(edge.rhsDir), edge.rhsGroup)
		if s.x == e.x && s.y == e.y && s.dx == e.dx {
			// An edge from a side back to the same side: part the two ends so it shows as a loop.
			s.x -= float64(8 * math.Abs(s.dy))
			s.y -= float64(8 * math.Abs(s.dx))
			e.x += float64(8 * math.Abs(s.dy))
			e.y += float64(8 * math.Abs(s.dx))
		}
		routed := route(&s, &e)
		if s.wire {
			routed = append([]float64{centerX(a), Y[nodeRow[a]] + half}, routed...)
		}
		if e.wire {
			routed = append(routed, centerX(b), Y[nodeRow[b]]+half)
		}
		points := simplify(routed)
		if len(points) < 4 {
			continue
		}
		geoEdge := -1
		if geo != nil {
			geoEdge = len(geo.Edges)
			geo.Edges = append(geo.Edges, geoEdgeOf(edge, a, b, points))
		}
		id := svg.EscText(edge.lhsId + "-" + edge.rhsId)
		startTrim, endTrim := 0.0, 0.0
		if edge.lhsInto && !s.wire {
			startTrim = trimLength + s.gap
		}
		if edge.rhsInto && !e.wire {
			endTrim = trimLength + e.gap
		}
		svg.WriteStrings(&edgesOut, `<g class="pele-edge" data-id="`, id, `"><path d="`)
		pathData(&edgesOut, points, startTrim, endTrim)
		edgesOut.WriteString(`"/>`)
		if edge.lhsInto {
			svg.WriteMarker(&edgesOut, "arrow_point", s.x+float64(s.dx*s.gap), s.y+float64(s.dy*s.gap), -s.dx, -s.dy, "var(--_l)")
		}
		if edge.rhsInto {
			svg.WriteMarker(&edgesOut, "arrow_point", e.x+float64(e.dx*e.gap), e.y+float64(e.dy*e.gap), -e.dx, -e.dy, "var(--_l)")
		}
		edgesOut.WriteString(`</g>`)
		best, bestLength := -1, -1.0
		flat, flatLength := -1, -1.0
		label := edgeLabels[k]
		for i := 0; i < len(points); i += 2 {
			minX = math.Min(minX, points[i]-4)
			maxX = math.Max(maxX, points[i]+4)
			minY = math.Min(minY, points[i+1]-4)
			maxY = math.Max(maxY, points[i+1]+4)
		}
		for i := 0; i+3 < len(points); i += 2 {
			horizontal := math.Abs(points[i+1]-points[i+3]) < 0.01
			length := math.Abs(points[i]-points[i+2]) + math.Abs(points[i+1]-points[i+3])
			if !horizontal {
				runs = append(runs, points[i], math.Min(points[i+1], points[i+3]), math.Max(points[i+1], points[i+3]))
			}
			if length > bestLength {
				best = i
				bestLength = length
			}
			if horizontal && length > flatLength {
				flat = i
				flatLength = length
			}
		}
		if label.Width > 0 {
			at := best
			if flatLength >= label.Width+8 {
				at = flat
			}
			x := (points[at] + points[at+2]) / 2
			y := (points[at+1] + points[at+3]) / 2
			w := label.Width + 8
			upright := math.Abs(points[at]-points[at+2]) < 0.01
			// On a short run next to a node the label would land on the node, so it moves off to the side.
			for _, p := range [2]*Port{&s, &e} {
				if x+w/2 <= p.x0 || x-w/2 >= p.x1 || y+label.Height/2 <= p.y0 || y-label.Height/2 >= p.y1 {
					continue
				}
				if upright {
					if x >= (p.x0+p.x1)/2 {
						x = p.x1 + w/2 + 4
					} else {
						x = p.x0 - w/2 - 4
					}
				} else if y >= (p.y0+p.y1)/2 {
					y = p.y1 + label.Height/2 + 4
				} else {
					y = p.y0 - label.Height/2 - 4
				}
			}
			minX = math.Min(minX, x-w/2)
			maxX = math.Max(maxX, x+w/2)
			minY = math.Min(minY, y-label.Height/2)
			maxY = math.Max(maxY, y+label.Height/2)
			labelsOut.WriteString(svg.EdgeLabelSvg(edge.lhsId+"-"+edge.rhsId, label, x, y, "", icons))
			if geoEdge >= 0 {
				geo.Edges[geoEdge].Label = label.Plain()
				geo.Edges[geoEdge].LabelX, geo.Edges[geoEdge].LabelY = x, y
			}
		}
	}

	var clusters strings.Builder
	var crossings []float64
	for g, group := range groups {
		x := left(g)
		y := top(g)
		w := right(g) - x
		label := groupLabels[g]
		svg.WriteStrings(&clusters, `<g class="pele-cluster" data-id="`, svg.EscText(group.id), `"><rect x="`)
		svg.WriteNum(&clusters, x)
		clusters.WriteString(`" y="`)
		svg.WriteNum(&clusters, y)
		clusters.WriteString(`" width="`)
		svg.WriteNum(&clusters, w)
		clusters.WriteString(`" height="`)
		svg.WriteNum(&clusters, bottom(g)-y)
		svg.WriteStrings(&clusters, `" fill="var(--_a)" fill-opacity="0.5" stroke="var(--_b)"`, svg.ROUNDED, `/>`)
		if headW[g] > 0 {
			// The title starts at the left, or after the edges that come down through that corner.
			band := y + headH[g] + 12
			crossings = crossings[:0]
			for i := 0; i < len(runs); i += 3 {
				if runs[i+1] < band && runs[i+2] > y && runs[i] > x && runs[i] < x+w {
					crossings = append(crossings, runs[i])
				}
			}
			slices.Sort(crossings)
			crossings = append(crossings, x+w-4)
			start := x + 10
			for _, crossing := range crossings {
				if crossing-6-start >= headW[g] {
					break
				}
				start = math.Max(start, crossing+6)
			}
			if start+headW[g] > x+w-4 {
				start = x + 10
			}
			cy := y + 6 + headH[g]/2
			textX := start
			if group.icon != "" {
				iconSlot(&clusters, group.icon, start, cy-badge/2, badge, icons)
				textX += badge + 6
			}
			clusters.WriteString(svg.LabelSvg(label, textX+label.Width/2, cy, ` class="pele-cluster-label" fill="var(--_m)"`, icons))
		}
		clusters.WriteString(`</g>`)
	}

	var nodesOut strings.Builder
	glyph := iconSize * 0.5
	textSize := math.Max(6, js.Round(size*0.75))
	for i, node := range nodes {
		kind := "pele-service"
		if node.typ == "junction" {
			kind = "pele-junction"
		}
		svg.WriteStrings(&nodesOut, `<g class="pele-node `, kind, `" data-id="`, svg.EscText(node.id), `" transform="translate(`)
		svg.WriteNum(&nodesOut, centerX(i))
		nodesOut.WriteByte(',')
		svg.WriteNum(&nodesOut, Y[nodeRow[i]])
		nodesOut.WriteString(`)">`)
		if node.typ == "junction" {
			nodesOut.WriteString(`<circle cy="`)
			svg.WriteNum(&nodesOut, half)
			svg.WriteStrings(&nodesOut, `" r="`, js.Int(DOT), `" fill="var(--_l)"/></g>`)
			continue
		}
		nodesOut.WriteString(`<rect x="`)
		svg.WriteNum(&nodesOut, -half)
		nodesOut.WriteString(`" width="`)
		svg.WriteNum(&nodesOut, iconSize)
		nodesOut.WriteString(`" height="`)
		svg.WriteNum(&nodesOut, iconSize)
		svg.WriteStrings(&nodesOut, `" fill="var(--_s)" stroke="var(--_b)"`, svg.ROUNDED, `/>`)

		if node.icon != "" {
			iconSlot(&nodesOut, node.icon, -glyph/2, (iconSize-glyph)/2, glyph, icons)
		} else if node.iconText != "" {
			label := text.LayoutLabel(node.iconText, false, measurer, textSize, iconSize-8, 0)
			lines := max(1, int(math.Floor((iconSize-4)/js.Or(label.LineHeight, 1))))
			nodesOut.WriteString(svg.LabelSvg(fit(label, iconSize-8, lines, measurer), 0, half, ` class="pele-icon-text" font-size="`+js.Num(textSize)+`"`, icons))
		}
		svg.WriteStrings(&nodesOut, svg.LabelSvg(labels[i], 0, iconSize+LABEL_GAP+labels[i].Height/2, ` class="pele-label"`, icons), `</g>`)
	}

	room := 4000.0
	if tight {
		room = common.TitleRoom(options)
	}
	title := text.LayoutLabel(model.title, false, measurer, size, room, text.Bold)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 12
	}
	width := math.Ceil(math.Max(maxX-minX, title.Width) + float64(2*pad))
	height := math.Ceil(maxY - minY + titleHeight + float64(2*pad))
	ox := js.Round((width-(maxX-minX))/2 - minX)
	oy := js.Round(pad + titleHeight - minY)
	var body strings.Builder
	if clusters.Len() > 0 {
		svg.WriteStrings(&body, `<g class="pele-clusters">`, clusters.String(), `</g>`)
	}
	if edgesOut.Len() > 0 {
		svg.WriteStrings(&body, `<g class="pele-edges" fill="none" stroke="var(--_l)" stroke-linecap="round" stroke-linejoin="round">`, edgesOut.String(), `</g>`)
	}
	if labelsOut.Len() > 0 {
		svg.WriteStrings(&body, `<g class="pele-edge-labels" font-size="`, js.Num(small), `">`, labelsOut.String(), `</g>`)
	}
	if nodesOut.Len() > 0 {
		svg.WriteStrings(&body, `<g class="pele-nodes">`, nodesOut.String(), `</g>`)
	}
	parts := []string{svg.LabelSvg(title, width/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil)}
	if body.Len() > 0 {
		parts = append(parts, `<g transform="translate(`, js.Num(ox), `,`, js.Num(oy), `)">`, body.String(), `</g>`)
	}
	out := svg.SvgDocument("architecture", width, height, size, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr}, parts...)
	rendered := types.Rendered{SVG: out, Width: width, Height: height, Links: []types.LinkInfo{}}
	if geo != nil {
		// Everything above is placed before the body's translate.
		geo.Width, geo.Height = width, height
		for g, group := range groups {
			x, y := left(g), top(g)
			geo.Nodes = append(geo.Nodes, types.GeoNode{ID: group.id, X: x + (right(g)-x)/2 + ox, Y: y + (bottom(g)-y)/2 + oy, W: right(g) - x, H: bottom(g) - y,
				Label: groupLabels[g].Plain(), Group: true, Parent: grid.groupParent[g]})
		}
		for i, node := range nodes {
			n := types.GeoNode{ID: node.id, Shape: "rect", X: centerX(i) + ox, Y: Y[nodeRow[i]] + half + oy, W: boxW[i], H: iconSize, Parent: grid.nodeParent[i]}
			switch {
			case node.typ == "junction":
				n.Shape = "f-circ"
			case node.title != "":
				n.Label = labels[i].Plain()
			case node.iconText != "":
				n.Label = []string{node.iconText}
			case node.icon != "":
				n.Label = []string{node.icon}
			default:
				n.Label = []string{node.id}
			}
			geo.Nodes = append(geo.Nodes, n)
		}
		for k := range geo.Edges {
			e := &geo.Edges[k]
			// A junction's ports are its centre: a step along the leg tells which side the edge leaves by.
			last := len(e.Route) - 3
			if nodes[e.From].typ == "junction" {
				nudge(e.Route[0:2], e.Route[3:5])
			}
			if nodes[e.To].typ == "junction" {
				nudge(e.Route[last:last+2], e.Route[last-3:last-1])
			}
			e.From += G
			e.To += G
			for j := 0; j < len(e.Route); j += 3 {
				e.Route[j] += ox
				e.Route[j+1] += oy
			}
			e.LabelX += ox
			e.LabelY += oy
		}
		rendered.Geometry = geo
	}
	return rendered
}

// nudge moves the point p one unit toward q.
func nudge(p, q []float64) {
	dx, dy := q[0]-p[0], q[1]-p[1]
	if l := js.Hypot(dx, dy); l > 0 {
		p[0] += dx / l
		p[1] += dy / l
	}
}

// geoEdgeOf gives an edge as sketch/text reads it: a route of x, y, axis triples, where an
// axis of 0 marks a leg that runs down. The groups come before the nodes, so the caller
// moves From and To past them. Not in pele.
func geoEdgeOf(edge ArchEdge, a, b int, points []float64) types.GeoEdge {
	e := types.GeoEdge{From: a, To: b, Stroke: "normal"}
	if edge.lhsInto {
		e.Start = "arrow_point"
	}
	if edge.rhsInto {
		e.End = "arrow_point"
	}
	for k := 0; k < len(points); k += 2 {
		axis := 1.0
		if k > 0 && math.Abs(points[k+1]-points[k-1]) > math.Abs(points[k]-points[k-2]) {
			axis = 0
		}
		e.Route = append(e.Route, points[k], points[k+1], axis)
	}
	return e
}
