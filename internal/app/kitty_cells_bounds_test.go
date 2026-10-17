package app

import (
	"testing"

	"tuios.dev/vt"
)

// TestKittyBoxFromHostileSizes holds the cell box of a placement to sane
// numbers whatever a guest writes into c=, r=, s=, v=, w= and h=. These come
// straight from a pane's output, so they are a security boundary: a guest must
// not be able to crash the client or hand the layout a box it cannot use.
//
// The ways this could fail, written down before the test:
//
//  1. A product of guest numbers wraps an int to zero and the division
//     panics: s=1<<62 with c=12 on 20px cells.
//  2. A product wraps to a negative or tiny number and the box comes out
//     with zero or negative cells on the side the guest left out.
//  3. A negative c= or r= reaches the box and counts as given.
//  4. A huge c= or r= reaches the box unbounded.
func TestKittyBoxFromHostileSizes(t *testing.T) {
	kp := &KittyPassthrough{caps: &HostCapabilities{CellWidth: 10, CellHeight: 20}}
	for _, tc := range []struct {
		name     string
		cmd      vt.KittyCommand
		min, max [2]int // cols, rows
	}{
		{"s past int range with c", vt.KittyCommand{Columns: 12, Width: 1 << 62, Height: 1}, [2]int{12, 1}, [2]int{12, 1}},
		{"v past int range with r", vt.KittyCommand{Rows: 3, Width: 1, Height: 1 << 62}, [2]int{1, 3}, [2]int{1, 3}},
		{"negative c", vt.KittyCommand{Columns: -5, Width: 512, Height: 512}, [2]int{52, 26}, [2]int{52, 26}},
		{"negative r", vt.KittyCommand{Rows: -5, Columns: 12, Width: 512, Height: 512}, [2]int{12, 6}, [2]int{12, 6}},
		{"huge c", vt.KittyCommand{Columns: 1 << 40, Width: 512, Height: 512}, [2]int{1, 1}, [2]int{maxBoxCells, maxBoxCells}},
		{"huge c and r", vt.KittyCommand{Columns: 1 << 40, Rows: 1 << 40}, [2]int{1, 1}, [2]int{maxBoxCells, maxBoxCells}},
		{"huge s and v", vt.KittyCommand{Width: 1 << 62, Height: 1 << 62}, [2]int{1, 1}, [2]int{maxBoxCells, maxBoxCells}},
		{"crop past the picture", vt.KittyCommand{Columns: 12, Width: 512, Height: 512, SourceX: 256, SourceWidth: 1 << 62, SourceHeight: 512}, [2]int{12, 12}, [2]int{12, 12}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, cols := kp.calculateImageCells(&tc.cmd)
			if cols < tc.min[0] || cols > tc.max[0] || rows < tc.min[1] || rows > tc.max[1] {
				t.Fatalf("box = %dx%d cells, want between %dx%d and %dx%d",
					cols, rows, tc.min[0], tc.min[1], tc.max[0], tc.max[1])
			}
		})
	}
}
