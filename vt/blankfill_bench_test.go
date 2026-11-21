package vt

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

const (
	blankFillWidth = 207
	blankCellBytes = 112
	blankRowBytes  = blankFillWidth * blankCellBytes
	blankColdRows  = 4096
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
