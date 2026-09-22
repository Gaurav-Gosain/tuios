package vt

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz/vtgen"
)

// extentHolds reports the first cell that breaks the grid's extent invariant:
// every cell of row y at or past ext[y] is a plain blank, and an unwritten row
// has an extent of zero. A cell past the extent that is not blank is one the
// scroll path would blank too little of, or leave out of the scrollback.
func extentHolds(g *grid) string {
	if len(g.ext) != len(g.rows) {
		return fmt.Sprintf("%d extents for %d rows", len(g.ext), len(g.rows))
	}
	for y, row := range g.rows {
		if row == nil {
			if g.ext[y] != 0 {
				return fmt.Sprintf("unwritten row %d has extent %d", y, g.ext[y])
			}
			continue
		}
		if g.ext[y] < 0 || g.ext[y] > g.width {
			return fmt.Sprintf("row %d has extent %d on a grid %d wide", y, g.ext[y], g.width)
		}
		for x := g.ext[y]; x < len(row); x++ {
			if !isBlankCell(&row[x]) {
				return fmt.Sprintf("row %d cell %d is %#v past the extent %d", y, x, row[x], g.ext[y])
			}
		}
	}
	return ""
}

// TestGridExtentHoldsUnderGeneratedInput drives the emulator with generated
// terminal input and checks the extent invariant on both screens after every
// step. The grid-level random test covers the grid's own operations; this one
// covers the writes that reach rows from outside it: the ASCII run's direct
// store, the cell shifts, and the whole-screen rotation.
func TestGridExtentHoldsUnderGeneratedInput(t *testing.T) {
	seeds := uint64(200)
	if testing.Short() {
		seeds = 40
	}
	for seed := range seeds {
		g := vtgen.New(seed)
		emu := NewEmulator(80, 24)
		for i, seq := range g.Script(300) {
			if seq.Kind == "resize" {
				emu.Resize(seq.Cols, seq.Rows)
			} else if _, err := emu.WriteString(seq.Bytes); err != nil {
				t.Fatal(err)
			}
			for s := range emu.scrs {
				if bad := extentHolds(emu.scrs[s].buf); bad != "" {
					t.Fatalf("seed %d step %d (%s), screen %d: %s", seed, i, seq.Desc, s, bad)
				}
			}
		}
	}
}
