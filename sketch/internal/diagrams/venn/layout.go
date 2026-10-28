// Ported from pele src/diagrams/venn/layout.ts at 8aff183be57021b533f319a29341a529be934b08.

package venn

import (
	"math"
	"slices"
	"strings"

	"tuios.dev/sketch/internal/js"
)

type Circle struct {
	id      string
	x, y, r float64
}

// Spot is where the content of a region goes: the point inside all of `inside` and outside every other
// circle that is farthest from their outlines, and how far that is. A margin of 0 or less means
// the circles as placed leave the region empty.
type Spot struct {
	x, y, margin float64
}

const TAU = math.Pi * 2

// setsKey is one key for a list of ids. An id never holds a double quote.
func setsKey(ids []string) string { return "\"" + strings.Join(ids, "\"") }

// ensurePairwiseSubsets: for every overlap of three or more sets, adds the pairwise overlaps it implies and the diagram
// does not state, a quarter of the smaller set each, so that the circles are drawn overlapping.
// Returns the list it was given when there is nothing to add.
func ensurePairwiseSubsets(subsets []VennSubset) []VennSubset {
	present := map[string]bool{}
	sizes := map[string]float64{}
	sorted := func(sets []string) []string {
		out := slices.Clone(sets)
		slices.SortStableFunc(out, js.Compare)
		return out
	}
	for _, subset := range subsets {
		present[setsKey(sorted(subset.sets))] = true
		if len(subset.sets) == 1 {
			sizes[subset.sets[0]] = subset.size
		}
	}
	var added []VennSubset
	for _, subset := range subsets {
		if len(subset.sets) < 3 {
			continue
		}
		members := sorted(subset.sets)
		for i := 0; i < len(members)-1; i++ {
			for j := i + 1; j < len(members); j++ {
				pair := []string{members[i], members[j]}
				key := setsKey(pair)
				if present[key] {
					continue
				}
				present[key] = true
				a, okA := sizes[pair[0]]
				b, okB := sizes[pair[1]]
				size := 2.5
				if okA && okB {
					size = math.Min(a, b) / 4
				}
				added = append(added, VennSubset{sets: pair, size: size, hasLabel: true})
			}
		}
	}
	if len(added) > 0 {
		return append(slices.Clone(subsets), added...)
	}
	return subsets
}

// lensArea is the area two circles share when their centers are d apart.
func lensArea(r1, r2, d float64) float64 {
	if d >= r1+r2 {
		return 0
	}
	small := math.Min(r1, r2)
	if d <= math.Abs(r1-r2) {
		return float64(math.Pi*small) * small
	}
	a1 := float64(float64(r1*r1) * js.Acos((float64(d*d)+float64(r1*r1)-float64(r2*r2))/float64(float64(2*d)*r1)))
	a2 := float64(float64(r2*r2) * js.Acos((float64(d*d)+float64(r2*r2)-float64(r1*r1))/float64(float64(2*d)*r2)))
	a3 := float64(0.5 * math.Sqrt(math.Max(float64(float64(float64((-d+r1+r2)*(d+r1-r2))*(d-r1+r2))*(d+r1+r2)), 0)))
	return a1 + a2 - a3
}

// distanceForOverlap is how far apart two circles sit for their overlap to have the given area. The area falls as the
// distance grows, so bisection finds it.
func distanceForOverlap(r1, r2, area float64) float64 {
	low := math.Abs(r1 - r2)
	high := r1 + r2
	if !(area > 0) {
		return high
	}
	m := math.Min(r1, r2)
	if area >= float64(math.Pi*float64(m*m)) {
		return low
	}
	for i := 0; i < 48; i++ {
		middle := (low + high) / 2
		if lensArea(r1, r2, middle) > area {
			low = middle
		} else {
			high = middle
		}
	}
	return (low + high) / 2
}

// margin is how far a point is from the nearest outline it must stay within or without. Gives up, with
// some value below `floor`, as soon as the point cannot reach `floor`.
func margin(circles []Circle, inside, outside []int, x, y, floor float64) float64 {
	best := math.Inf(1)
	for _, i := range inside {
		c := &circles[i]
		dx := x - c.x
		dy := y - c.y
		room := c.r - math.Sqrt(float64(dx*dx)+float64(dy*dy))
		if room < best {
			if room < floor {
				return room
			}
			best = room
		}
	}
	for _, i := range outside {
		c := &circles[i]
		dx := x - c.x
		dy := y - c.y
		room := math.Sqrt(float64(dx*dx)+float64(dy*dy)) - c.r
		if room < best {
			if room < floor {
				return room
			}
			best = room
		}
	}
	return best
}

// findSpot finds the roomiest point of a region by sampling the smallest of its circles on a grid and then
// walking uphill from the best sample. Ties go to the point nearest the middle of the region, so
// a symmetric arrangement gets symmetric labels.
func findSpot(circles []Circle, inside, outside []int) Spot {
	cx, cy := 0.0, 0.0
	smallest := circles[inside[0]]
	n := float64(len(inside))
	for _, i := range inside {
		c := circles[i]
		cx += c.x / n
		cy += c.y / n
		if c.r < smallest.r {
			smallest = c
		}
	}
	r := smallest.r
	x, y := cx, cy
	best := margin(circles, inside, outside, x, y, math.Inf(-1))
	off := 0.0
	tiny := float64(r * 1e-9)
	consider := func(px, py float64) bool {
		m := margin(circles, inside, outside, px, py, best-tiny)
		if !(m > best-tiny) {
			return false
		}
		distance := js.Hypot(px-cx, py-cy)
		if m > best+tiny || distance < off-tiny {
			best = m
			off = distance
			x = px
			y = py
			return true
		}
		return false
	}
	const GRID = 10
	for gy := -GRID; gy <= GRID; gy++ {
		for gx := -GRID; gx <= GRID; gx++ {
			if gx*gx+gy*gy <= GRID*GRID {
				consider(smallest.x+float64(float64(gx)*r)/GRID, smallest.y+float64(float64(gy)*r)/GRID)
			}
		}
	}
	step := r / GRID
	for round := 0; round < 40 && step > float64(r*1e-6); round++ {
		moved := false
		for k := 0; k < 8; k++ {
			angle := float64(float64(k)*TAU) / 8
			if consider(x+float64(js.Cos(angle)*step), y+float64(js.Sin(angle)*step)) {
				moved = true
			}
		}
		if !moved {
			step /= 2
		}
	}
	return Spot{x, y, best}
}

// layoutCircles places one circle per set, with areas in proportion to the sizes of the sets and each pair as
// far apart as its overlap asks: one, two and three sets exactly, more by relaxing a ring of
// circles toward those distances. Lengths are in units of the square root of a size.
func layoutCircles(subsets []VennSubset) []Circle {
	var ids []string
	sizes := map[string]float64{}
	for _, subset := range subsets {
		for _, id := range subset.sets {
			if _, ok := sizes[id]; !ok {
				sizes[id] = 10
				ids = append(ids, id)
			}
		}
	}
	for _, subset := range subsets {
		if len(subset.sets) == 1 {
			sizes[subset.sets[0]] = subset.size
		}
	}

	index := map[string]int{}
	circles := make([]Circle, 0, len(ids))
	largest := 0.0
	finite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
	for _, id := range ids {
		if size := sizes[id]; finite(size) && size > largest {
			largest = size
		}
	}
	if largest == 0 {
		largest = 10
	}
	widest := math.Sqrt(largest / math.Pi)
	for _, id := range ids {
		size := sizes[id]
		index[id] = len(circles)
		// A set too small to hold its name, or with no size at all, still gets a circle that shows.
		r := 0.0
		if finite(size) && size > 0 {
			r = math.Sqrt(size / math.Pi)
		}
		circles = append(circles, Circle{id: id, r: math.Max(r, float64(widest*0.3))})
	}
	n := len(circles)
	if n < 2 {
		return circles
	}

	// The overlap each pair was given, by the indexes of its circles.
	overlaps := map[int]float64{}
	var overlapOrder []int
	for _, subset := range ensurePairwiseSubsets(subsets) {
		if len(subset.sets) != 2 {
			continue
		}
		a := index[subset.sets[0]]
		b := index[subset.sets[1]]
		if a != b {
			key := min(a, b)*n + max(a, b)
			if _, ok := overlaps[key]; !ok {
				overlapOrder = append(overlapOrder, key)
			}
			overlaps[key] = subset.size
		}
	}
	// How far apart each overlapping pair should be. Pairs that do not overlap only have to keep a gap.
	wanted := map[int]float64{}
	for _, key := range overlapOrder {
		wanted[key] = distanceForOverlap(circles[key/n].r, circles[key%n].r, overlaps[key])
	}
	gap := float64(widest * 0.15)
	distance := func(a, b int) float64 {
		if d, ok := wanted[a*n+b]; ok {
			return d
		}
		return circles[a].r + circles[b].r + gap
	}

	if n == 2 {
		circles[1].x = distance(0, 1)
		return circles
	}

	if n == 3 {
		// Two sets side by side and the third below, the corners of a triangle whose sides are the
		// three distances. An overlap of all three has to exist, so the triangle shrinks until it does.
		ab := distance(0, 1)
		ac := distance(0, 2)
		bc := distance(1, 2)
		triple := false
		for _, subset := range subsets {
			if distinct(subset.sets) == 3 {
				triple = true
			}
		}
		for attempt := 0; attempt < 40; attempt++ {
			x := 0.0
			if ab > 0 {
				x = (float64(ab*ab) + float64(ac*ac) - float64(bc*bc)) / float64(2*ab)
			}
			circles[1].x = ab
			circles[2].x = math.Max(-ac, math.Min(ac, x))
			circles[2].y = math.Sqrt(math.Max(float64(ac*ac)-float64(x*x), 0))
			if !triple || findSpot(circles, []int{0, 1, 2}, nil).margin > float64(widest*0.12) {
				break
			}
			ab *= 0.94
			ac *= 0.94
			bc *= 0.94
		}
		return circles
	}

	total := 0.0
	for _, c := range circles {
		total += float64(2*c.r) + gap
	}
	rounds := min(300, int(math.Floor(3e6/float64(n*n))))
	if n <= 64 {
		ring := total / TAU
		for i := range circles {
			angle := float64(float64(i)*TAU)/float64(n) - math.Pi/2
			circles[i].x = float64(js.Cos(angle) * ring)
			circles[i].y = float64(js.Sin(angle) * ring)
		}
	} else {
		columns := int(math.Ceil(math.Sqrt(float64(n))))
		cell := float64(2*widest) + gap
		for i := range circles {
			circles[i].x = float64(float64(i%columns) * cell)
			circles[i].y = float64(float64(i/columns) * cell)
		}
	}
	// Each pair's wanted distance, NaN for none, in a table when there is room for one.
	var dense []float64
	if n*n <= 1<<16 {
		dense = make([]float64, n*n)
		for i := range dense {
			dense[i] = math.NaN()
		}
		for key, d := range wanted {
			dense[key] = d
		}
	}
	dx := make([]float64, n)
	dy := make([]float64, n)
	pulls := make([]int, n)
	for round := 0; round < rounds; round++ {
		clear(dx)
		clear(dy)
		clear(pulls)
		for a := 0; a < n; a++ {
			for b := a + 1; b < n; b++ {
				var overlap float64
				var has bool
				if dense != nil {
					overlap = dense[a*n+b]
					has = !math.IsNaN(overlap)
				} else {
					overlap, has = wanted[a*n+b]
				}
				apart := circles[a].r + circles[b].r + gap
				ux := circles[b].x - circles[a].x
				uy := circles[b].y - circles[a].y
				length := js.Hypot(ux, uy)
				if !has && length >= apart {
					continue
				}
				if length < 1e-9 {
					ux = js.Cos(float64(a + b))
					uy = js.Sin(float64(a + b))
					length = 1
				}
				target := apart
				if has {
					target = overlap
				}
				move := (length - target) / 2 / length
				dx[a] += float64(ux * move)
				dy[a] += float64(uy * move)
				dx[b] -= float64(ux * move)
				dy[b] -= float64(uy * move)
				pulls[a]++
				pulls[b]++
			}
		}
		for i := 0; i < n; i++ {
			if pulls[i] == 0 {
				continue
			}
			circles[i].x += dx[i] / float64(pulls[i])
			circles[i].y += dy[i] / float64(pulls[i])
		}
	}
	return circles
}

// distinct is new Set(list).size.
func distinct(list []string) int {
	n := 0
	for i, s := range list {
		if !slices.Contains(list[:i], s) {
			n++
		}
	}
	return n
}

type corner struct {
	x, y float64
	a, b *Circle
}

// regionPath is the outline of the region all the given circles share, as an SVG path, or "" if they share none.
func regionPath(circles []*Circle, format func(float64) string) string {
	whole := func(c *Circle) string {
		r := format(c.r)
		return "M" + format(c.x-c.r) + "," + format(c.y) + "A" + r + "," + r + " 0 1 1 " + format(c.x+c.r) + "," + format(c.y) +
			"A" + r + "," + r + " 0 1 1 " + format(c.x-c.r) + "," + format(c.y) + "Z"
	}
	if len(circles) == 1 {
		return whole(circles[0])
	}
	within := func(x, y float64, skip, other *Circle) bool {
		for _, c := range circles {
			if c != skip && c != other && js.Hypot(x-c.x, y-c.y) > float64(c.r*(1+1e-9)) {
				return false
			}
		}
		return true
	}
	// The corners of the region: where two outlines cross inside all the other circles.
	var corners []corner
	for i := 0; i < len(circles); i++ {
		for j := i + 1; j < len(circles); j++ {
			a := circles[i]
			b := circles[j]
			d := js.Hypot(b.x-a.x, b.y-a.y)
			if d >= a.r+b.r || d <= math.Abs(a.r-b.r) || d == 0 {
				continue
			}
			along := (float64(a.r*a.r) - float64(b.r*b.r) + float64(d*d)) / float64(2*d)
			up := math.Sqrt(math.Max(float64(a.r*a.r)-float64(along*along), 0))
			mx := a.x + float64((b.x-a.x)*along)/d
			my := a.y + float64((b.y-a.y)*along)/d
			for _, sign := range [2]float64{1, -1} {
				x := mx + float64(float64(sign*up)*(b.y-a.y))/d
				y := my - float64(float64(sign*up)*(b.x-a.x))/d
				if within(x, y, a, b) {
					corners = append(corners, corner{x, y, a, b})
				}
			}
		}
	}
	if len(corners) < 2 {
		// No corners: either one circle lies inside all the others, or there is nothing shared.
		smallest := circles[0]
		for _, c := range circles {
			if c.r < smallest.r {
				smallest = c
			}
		}
		for _, c := range circles {
			if c != smallest && js.Hypot(c.x-smallest.x, c.y-smallest.y)+smallest.r > float64(c.r*(1+1e-9)) {
				return ""
			}
		}
		return whole(smallest)
	}
	cx, cy := 0.0, 0.0
	count := float64(len(corners))
	for _, p := range corners {
		cx += p.x / count
		cy += p.y / count
	}
	js.Sort(corners, func(p, q corner) float64 { return js.Atan2(p.y-cy, p.x-cx) - js.Atan2(q.y-cy, q.x-cx) })
	var d strings.Builder
	for i := range corners {
		from := corners[i]
		to := corners[(i+1)%len(corners)]
		if i == 0 {
			d.WriteString("M" + format(from.x) + "," + format(from.y))
		}
		// The side between two corners is an arc of a circle that passes through both; of those, the
		// one whose arc stays inside the others.
		var arc *Circle
		large := 0
		narrowest := math.Inf(1)
		for _, c := range [2]*Circle{from.a, from.b} {
			if c != to.a && c != to.b {
				continue
			}
			start := js.Atan2(from.y-c.y, from.x-c.x)
			sweep := js.Atan2(to.y-c.y, to.x-c.x) - start
			if sweep <= 0 {
				sweep += TAU
			}
			mx := c.x + float64(c.r*js.Cos(start+sweep/2))
			my := c.y + float64(c.r*js.Sin(start+sweep/2))
			width := js.Hypot(mx-(from.x+to.x)/2, my-(from.y+to.y)/2)
			if width < narrowest {
				narrowest = width
				arc = c
				large = 0
				if sweep > math.Pi {
					large = 1
				}
			}
		}
		if arc == nil {
			d.WriteString("L" + format(to.x) + "," + format(to.y))
		} else {
			fmtLarge := "0"
			if large == 1 {
				fmtLarge = "1"
			}
			d.WriteString("A" + format(arc.r) + "," + format(arc.r) + " 0 " + fmtLarge + " 1 " + format(to.x) + "," + format(to.y))
		}
	}
	d.WriteString("Z")
	return d.String()
}
