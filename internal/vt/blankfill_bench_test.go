package vt

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// The earlier perf pass attributed most of a flood's CPU to blanking the rows
// arriving at the bottom of the screen during a scroll, tried replacing the
// per-cell store with one bulk copy from a prototype row, measured ~6% and
// reverted it, and concluded the loop was already running at memory bandwidth.
// That is a claim worth checking rather than inheriting, because "we are at the
// memory wall" is the conclusion that stops anyone looking again.
//
// uv.Cell is 112 bytes and carries three strings (Content, and Link's two), so
// blanking a cell is a pointer-carrying assignment. It cannot compile to a
// memset the way zeroing can, and it takes a write barrier while the GC marks.
// That is the mechanism these separate:
//
//	fill-loop   the store per cell that rotateWholeScreenUp actually does
//	fill-copy   one bulk copy from a prototype row (the reverted change)
//	fill-zero   the same loop storing the zero Cell, which the compiler may
//	            turn into a memclr because no pointers are written
//	byte-move   copy() over the same byte count with no pointers at all
//
// The cold variants walk a working set far larger than L2, which is the shape
// the real path has: the row being blanked was just recycled out of the
// scrollback ring, so it is not sitting in L1 the way a benchmark that reuses
// one row would leave it.
const (
	blankFillWidth = 207
	blankCellBytes = 112
	blankRowBytes  = blankFillWidth * blankCellBytes
	// Enough rows to blow past a 16 MB L3.
	blankColdRows = 4096
)

func BenchmarkBlankFill(b *testing.B) {
	blank := uv.EmptyCell

	proto := make(uv.Line, blankFillWidth)
	for x := range proto {
		proto[x] = blank
	}

	b.Run("hot/fill-loop", func(b *testing.B) {
		row := make(uv.Line, blankFillWidth)
		b.SetBytes(blankRowBytes)
		for b.Loop() {
			for x := range row {
				row[x] = blank
			}
		}
	})

	b.Run("hot/fill-copy", func(b *testing.B) {
		row := make(uv.Line, blankFillWidth)
		b.SetBytes(blankRowBytes)
		for b.Loop() {
			copy(row, proto)
		}
	})

	b.Run("hot/fill-zero", func(b *testing.B) {
		row := make(uv.Line, blankFillWidth)
		var zero uv.Cell
		b.SetBytes(blankRowBytes)
		for b.Loop() {
			for x := range row {
				row[x] = zero
			}
		}
	})

	b.Run("hot/byte-move", func(b *testing.B) {
		dst := make([]byte, blankRowBytes)
		src := make([]byte, blankRowBytes)
		b.SetBytes(blankRowBytes)
		for b.Loop() {
			copy(dst, src)
		}
	})

	// Cold: a fresh row each iteration out of a working set too big to cache,
	// which is what the scrollback ring hands back mid-flood.
	rows := make([]uv.Line, blankColdRows)
	backing := make(uv.Line, blankColdRows*blankFillWidth)
	for i := range rows {
		rows[i] = backing[i*blankFillWidth : (i+1)*blankFillWidth]
	}

	b.Run("cold/fill-loop", func(b *testing.B) {
		b.SetBytes(blankRowBytes)
		i := 0
		for b.Loop() {
			row := rows[i&(blankColdRows-1)]
			for x := range row {
				row[x] = blank
			}
			i++
		}
	})

	b.Run("cold/fill-copy", func(b *testing.B) {
		b.SetBytes(blankRowBytes)
		i := 0
		for b.Loop() {
			copy(rows[i&(blankColdRows-1)], proto)
			i++
		}
	})
}
