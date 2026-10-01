package layout

import "github.com/Gaurav-Gosain/tuios/internal/config"

// MasterParams is everything the master-stack tiler reads besides the region
// and the pane count.
//
// The shape follows dwm and its descendants. The first Count panes are the
// masters and share one column (or one row, with the master on the top or the
// bottom). The rest are the stack and share what is left. Position names the
// side the masters take. Ratio is the masters' share of the region along the
// axis that separates them from the stack.
type MasterParams struct {
	// Position is one of the config.MasterPosition* values. Anything else,
	// the empty string included, is left.
	Position string
	// Count is how many panes are masters. Zero and below mean one, and a
	// count above the pane count makes every pane a master.
	Count int
	// Ratio is the masters' share. Zero and below mean the default, and the
	// value is clamped to MinSplitRatio..MaxSplitRatio.
	Ratio float64
	// StackRatio is the first stack pane's share when the stack holds exactly
	// two panes in one column or row. Zero and below mean an equal split.
	StackRatio float64
	// Grid lays four or more panes out as an equal grid instead, but only with
	// one master on the left. That is the layout as it was before positions
	// and counts existed, and the default keeps it so nobody's screen changes
	// on an upgrade. Any other position or count asks for a master, and gets
	// one at every pane count.
	Grid bool
	// Gap is the cells kept between neighbours, as for spans.
	Gap int
}

// masterShape is what the tiler resolved the parameters to for one pane count
// and one region, after the fallbacks below.
type masterShape struct {
	grid     bool
	position string
	masters  int
	stack    int
}

// resolveMasterShape applies the fallbacks:
//
//   - The grid, under the conditions MasterParams.Grid names.
//   - Center with fewer than two stack panes is left. A centred master with one
//     pane beside it leaves an empty column on the other side, so the master
//     takes the left and the space goes to the stack, as Hyprland's center
//     orientation does by default.
//   - Two panes on a region that is taller than it is wide, as drawn, stack
//     one above the other: left becomes top and right becomes bottom. That is
//     what two panes always did on a tall screen.
func resolveMasterShape(n, w, h int, p MasterParams) masterShape {
	pos := config.ValidMasterPosition(p.Position)
	k := min(max(p.Count, 1), max(n, 1))
	s := masterShape{position: pos, masters: k, stack: max(n-k, 0)}
	if p.Grid && pos == config.MasterPositionLeft && k == 1 && n >= 4 {
		s.grid = true
		return s
	}
	if pos == config.MasterPositionCenter && s.stack < 2 {
		s.position = config.MasterPositionLeft
	}
	if n == 2 && k == 1 && !MasterStackSideBySide(w, h) {
		switch s.position {
		case config.MasterPositionLeft:
			s.position = config.MasterPositionTop
		case config.MasterPositionRight:
			s.position = config.MasterPositionBottom
		}
	}
	return s
}

// along reports whether a position separates the masters from the stack along
// the X axis.
func alongX(pos string) bool {
	return pos != config.MasterPositionTop && pos != config.MasterPositionBottom
}

// CalculateMasterLayout returns the master-stack rectangles for n panes in the
// region w by h whose first row is top. The rectangles are in pane order:
// masters first, then the stack.
//
// Every rectangle is inside the region and disjoint from every other one, at
// any size and any pane count, which is the contract CalculateMasterStackLayout
// states at length.
func CalculateMasterLayout(n, w, h, top int, p MasterParams) []TileLayout {
	if n <= 0 {
		return nil
	}
	if n == 1 {
		return []TileLayout{{X: 0, Y: top, Width: w, Height: h}}
	}
	ratio := p.Ratio
	if ratio <= 0 {
		ratio = float64(config.MasterRatioDefault) / 100
	}
	ratio = clampSplitRatio(ratio)

	shape := resolveMasterShape(n, w, h, p)
	if shape.grid {
		return gridLayout(n, w, h, top, p.Gap)
	}

	// Work in a frame where "along" is the axis between the masters and the
	// stack and "across" is the other one, then turn the result back.
	alongLen, acrossLen, alongOrigin, acrossOrigin := w, h, 0, top
	if !alongX(shape.position) {
		alongLen, acrossLen, alongOrigin, acrossOrigin = h, w, top, 0
	}
	place := func(along, across span) TileLayout {
		if alongX(shape.position) {
			return TileLayout{X: along.Pos, Y: across.Pos, Width: along.Size, Height: across.Size}
		}
		return TileLayout{X: across.Pos, Y: along.Pos, Width: across.Size, Height: along.Size}
	}

	out := make([]TileLayout, 0, n)
	if shape.stack == 0 {
		// Every pane is a master: they share the region across the axis, the
		// way the master column holds more than one master.
		for _, c := range spans(acrossOrigin, acrossLen, n, p.Gap) {
			out = append(out, place(span{Pos: alongOrigin, Size: alongLen}, c))
		}
		return out
	}

	stackCells := func(count int) []span {
		if count == 2 && p.StackRatio > 0 {
			a, b := splitByRatio(acrossOrigin, acrossLen, clampSplitRatio(p.StackRatio), p.Gap)
			return []span{a, b}
		}
		return spans(acrossOrigin, acrossLen, count, p.Gap)
	}

	if shape.position == config.MasterPositionCenter {
		left, master, right := splitThree(alongOrigin, alongLen, ratio, p.Gap)
		for _, c := range spans(acrossOrigin, acrossLen, shape.masters, p.Gap) {
			out = append(out, place(master, c))
		}
		// The stack is dealt right, left, right, left, so the two sides never
		// differ by more than one pane and a new pane lands on the side with
		// fewer.
		rightCount := (shape.stack + 1) / 2
		leftCount := shape.stack / 2
		rightCells := spans(acrossOrigin, acrossLen, rightCount, p.Gap)
		leftCells := spans(acrossOrigin, acrossLen, leftCount, p.Gap)
		for i := range shape.stack {
			if i%2 == 0 {
				out = append(out, place(right, rightCells[i/2]))
			} else {
				out = append(out, place(left, leftCells[i/2]))
			}
		}
		return out
	}

	near, far := splitByRatio(alongOrigin, alongLen, ratio, p.Gap)
	master, stack := near, far
	if shape.position == config.MasterPositionRight || shape.position == config.MasterPositionBottom {
		// The masters keep the size the ratio gives them and move to the far
		// end, so the ratio means the same share on either side.
		stack = span{Pos: alongOrigin, Size: far.Size}
		master = span{Pos: alongOrigin + far.Size + (far.Pos - near.Pos - near.Size), Size: near.Size}
	}
	for _, c := range spans(acrossOrigin, acrossLen, shape.masters, p.Gap) {
		out = append(out, place(master, c))
	}
	for _, c := range stackCells(shape.stack) {
		out = append(out, place(stack, c))
	}
	return out
}

// splitThree divides an extent into a left side, a middle that takes ratio of
// what is left once the two gaps are reserved, and a right side. The sides
// share the rest, the right one taking the odd cell, because the stack deals
// its first pane there and so it never holds fewer panes than the left. Every
// part keeps at least one cell. A gap too wide for that gives up ground first,
// as in spans.
func splitThree(origin, total int, ratio float64, gap int) (left, middle, right span) {
	if total-2*gap < 3 {
		gap = max((total-3)/2, 0)
	}
	avail := max(total-2*gap, 3)
	mid := min(max(int(float64(avail)*ratio), 1), avail-2)
	rest := avail - mid
	l := rest / 2
	left = span{Pos: origin, Size: l}
	middle = span{Pos: origin + l + gap, Size: mid}
	right = span{Pos: middle.Pos + mid + gap, Size: rest - l}
	return left, middle, right
}

// gridLayout is the equal-share grid four or more panes take with the defaults.
// Four panes is the 2x2 case of it.
func gridLayout(n, w, h, top, gap int) []TileLayout {
	cols := gridColumns(n)
	rowCount := (n + cols - 1) / cols
	rows := spans(top, h, rowCount, gap)
	out := make([]TileLayout, 0, n)
	for row := range rowCount {
		// The last row carries whatever is left over, which can be fewer panes
		// than a full row. They share the width between them rather than
		// leaving a hole where the missing ones would have been.
		inRow := min(cols, n-row*cols)
		cells := spans(0, w, inRow, gap)
		for col := range inRow {
			out = append(out, TileLayout{X: cells[col].Pos, Y: rows[row].Pos, Width: cells[col].Size, Height: rows[row].Size})
		}
	}
	return out
}

// MasterRatiosFrom reads the ratios back off panes the master-stack tiler laid
// out in region and something then resized, so a resize survives the next
// retile. It is the inverse of CalculateMasterLayout: the ratio it returns lays
// the masters out at the size they have now.
//
// stackRatio is zero when the stack does not hold exactly two panes in one
// column or row, or when they are not where the tiler would put them. ok is
// false when the panes are not in the shape the parameters describe (a grid, a
// zoom, panes out of their slots), and then there is nothing to record.
func MasterRatiosFrom(rects []Rect, region Rect, p MasterParams) (ratio, stackRatio float64, ok bool) {
	n := len(rects)
	shape := resolveMasterShape(n, region.W, region.H, p)
	if n < 2 || shape.grid || shape.stack == 0 {
		return 0, 0, false
	}
	gap := p.Gap

	// The same frame CalculateMasterLayout works in.
	type seg struct{ pos, size, crossPos, crossSize int }
	frame := func(r Rect) seg {
		if alongX(shape.position) {
			return seg{r.X, r.W, r.Y, r.H}
		}
		return seg{r.Y, r.H, r.X, r.W}
	}
	alongOrigin, alongLen, acrossOrigin, acrossLen := region.X, region.W, region.Y, region.H
	if !alongX(shape.position) {
		alongOrigin, alongLen, acrossOrigin, acrossLen = region.Y, region.H, region.X, region.W
	}

	master := frame(rects[0])
	for _, r := range rects[1:shape.masters] {
		if s := frame(r); s.pos != master.pos || s.size != master.size {
			return 0, 0, false
		}
	}
	stack := make([]seg, 0, shape.stack)
	for _, r := range rects[shape.masters:] {
		stack = append(stack, frame(r))
	}
	before := func(a, b seg) bool { return a.pos+a.size <= b.pos }

	switch shape.position {
	case config.MasterPositionLeft, config.MasterPositionTop:
		if master.pos != alongOrigin {
			return 0, 0, false
		}
		for _, s := range stack {
			if !before(master, s) || s.pos+s.size != alongOrigin+alongLen {
				return 0, 0, false
			}
		}
		ratio = SplitRatioFor(master.size, alongLen, gap)
	case config.MasterPositionRight, config.MasterPositionBottom:
		if master.pos+master.size != alongOrigin+alongLen {
			return 0, 0, false
		}
		for _, s := range stack {
			if !before(s, master) || s.pos != alongOrigin {
				return 0, 0, false
			}
		}
		ratio = SplitRatioFor(master.size, alongLen, gap)
	case config.MasterPositionCenter:
		for i, s := range stack {
			if (i%2 == 0 && !before(master, s)) || (i%2 == 1 && !before(s, master)) {
				return 0, 0, false
			}
		}
		// splitThree reserves two gaps; SplitRatioFor reserves one of
		// whatever total it is handed.
		g := gap
		if alongLen-2*g < 3 {
			g = max((alongLen-3)/2, 0)
		}
		ratio = SplitRatioFor(master.size, alongLen-g, g)
		return ratio, 0, true
	}

	if len(stack) == 2 {
		a, b := stack[0], stack[1]
		if a.crossPos == acrossOrigin && b.crossPos >= a.crossPos+a.crossSize &&
			b.crossPos+b.crossSize == acrossOrigin+acrossLen {
			stackRatio = SplitRatioFor(a.crossSize, acrossLen, gap)
		}
	}
	return ratio, stackRatio, true
}
