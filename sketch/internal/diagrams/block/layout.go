// Ported from pele src/diagrams/block/layout.ts at 8aff183be57021b533f319a29341a529be934b08.

package block

import (
	"math"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

type Cell struct {
	children []*Cell
	// Grid width of this container: a positive count, or -1 to put every child on one row.
	columns float64
	span    float64
	space   bool
	// Height reserved above a container's grid for its title.
	head float64
	// Size the content needs. Set by the caller for leaves, computed here for containers.
	nw float64
	nh float64
	// Placement: grid position, then center and size in pixels.
	col     float64
	row     int
	cols    float64
	heights []float64
	// Extra space after each row or column. nil is pele's shared NO_GAPS.
	rowGaps []float64
	colGaps []float64
	x       float64
	y       float64
	w       float64
	h       float64
}

const MAX_SPAN = 1000

func isInteger(x float64) bool {
	return !math.IsInf(x, 0) && x == math.Trunc(x)
}

func calculateBlockPosition(columns, position float64) (px, py float64) {
	if columns == 0 || !isInteger(columns) {
		types.Throw("Columns must be an integer !== 0.", types.CodeSemantic, types.ErrorInfo{Type: "block"})
	}
	if position < 0 || !isInteger(position) {
		types.Throw("Position must be a non-negative integer."+js.Num(position), types.CodeSemantic, types.ErrorInfo{Type: "block"})
	}
	if columns < 0 {
		return position, 0
	}
	return math.Mod(position, columns), math.Floor(position / columns)
}

// measure puts each child in its grid cell and sizes the container. Every column is as wide as the widest
// block needs. Every row that holds a plain block is as tall as the tallest block in the container,
// as in Mermaid; a row of nothing but containers is only as tall as they need.
func measure(box *Cell, gap, inset float64) {
	columns := box.columns
	var heights []float64
	position := 0.0
	cols := 0.0
	cw := 0.0
	tallest := 0.0
	for _, child := range box.children {
		px, fy := calculateBlockPosition(columns, position)
		py := int(fy)
		// Mermaid lets a block wider than the rest of its row stick out of the grid. Here it stops at the last column.
		span := 1.0
		if child.span >= 1 {
			span = math.Min(child.span, MAX_SPAN)
		}
		if columns > 0 && span > columns-px {
			span = columns - px
		}
		child.span = span
		child.col = px
		child.row = py
		position += span
		if px+span > cols {
			cols = px + span
		}
		if py == len(heights) {
			heights = append(heights, 0)
		}
		if len(child.children) == 0 {
			heights[py] = -1
		} else if heights[py] >= 0 && child.nh > heights[py] {
			heights[py] = child.nh
		}
		if child.space {
			continue
		}
		width := (child.nw - float64(gap*(span-1))) / span
		if width > cw {
			cw = width
		}
		if child.nh > tallest {
			tallest = child.nh
		}
	}
	height := float64(float64(len(heights)-1)*gap) + total(box.rowGaps)
	for row := range heights {
		if heights[row] < 0 {
			heights[row] = tallest
		}
		height += heights[row]
	}
	box.cols = cols
	box.heights = heights
	box.nw = math.Max(box.nw, float64(cols*cw)+float64((cols-1)*gap)+total(box.colGaps)+float64(2*inset))
	box.nh = height + float64(2*inset) + box.head
}

// arrange gives each child its cell, sharing out any room the container has beyond what it needs.
func arrange(box *Cell, gap, inset float64) {
	cols, heights, rowGaps, colGaps := box.cols, box.heights, box.rowGaps, box.colGaps
	rows := float64(len(heights))
	cw := (box.w - float64(2*inset) - float64((cols-1)*gap) - total(colGaps)) / cols
	room := box.h - float64(2*inset) - box.head - float64((rows-1)*gap) - total(rowGaps)
	var offsets []float64
	if len(colGaps) > 0 {
		sum := 0.0
		for col := 0; col < int(cols); col++ {
			offsets = append(offsets, sum)
			sum += gapAt(colGaps, col)
		}
	}
	needed := 0.0
	for _, height := range heights {
		needed += height
	}
	scale := 0.0
	share := room / rows
	if needed > 0 {
		scale = room / needed
		share = 0
	}
	left := box.x - box.w/2 + inset
	top := box.y - box.h/2 + inset + box.head
	row := 0
	for _, child := range box.children {
		for ; row < child.row; row++ {
			top += float64(heights[row]*scale) + share + gap + gapAt(rowGaps, row)
		}
		before := gapAt(offsets, int(child.col))
		child.w = float64(cw*child.span) + float64(gap*(child.span-1)) + gapAt(offsets, int(child.col+child.span-1)) - before
		child.h = float64(heights[row]*scale) + share
		child.x = left + float64(child.col*(cw+gap)) + before + child.w/2
		child.y = top + child.h/2
	}
}

func total(gaps []float64) float64 {
	sum := 0.0
	for _, gap := range gaps {
		sum += gap
	}
	return sum
}

// gapAt is TS `gaps[i] ?? 0`: a gap past the end of the list, or a hole in it, is 0. Not in pele.
func gapAt(gaps []float64, i int) float64 {
	if i < 0 || i >= len(gaps) {
		return 0
	}
	return gaps[i]
}

// layout lays out a tree of cells in place. The root has no border, so its grid starts at the origin.
func layout(root *Cell, gap float64) (width, height float64) {
	order := []*Cell{root}
	for i := 0; i < len(order); i++ {
		order = append(order, order[i].children...)
	}
	for i := len(order) - 1; i >= 0; i-- {
		box := order[i]
		if len(box.children) > 0 {
			inset := gap
			if i == 0 {
				inset = 0
			}
			measure(box, gap, inset)
		}
	}
	root.w = root.nw
	root.h = root.nh
	root.x = root.w / 2
	root.y = root.h / 2
	for i, box := range order {
		if len(box.children) > 0 {
			inset := gap
			if i == 0 {
				inset = 0
			}
			arrange(box, gap, inset)
		}
	}
	return root.w, root.h
}
