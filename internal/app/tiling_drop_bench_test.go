package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

// BenchmarkDropZoneMotion is the drop-zone work of one motion event in a BSP
// drag whose pointer stays over one edge of a neighbour. The drag path is kept
// flat in the pane count (see layout.BSPTree.ApplyLayoutInto), and the landing
// rectangle is worked out again only when the target or the edge changes, so
// this should allocate nothing at any pane count.
func BenchmarkDropZoneMotion(b *testing.B) {
	for _, n := range []int{4, 9} {
		b.Run(fmt.Sprintf("panes-%d", n), func(b *testing.B) {
			m := tiledKeystrokeOS(b, n, realCols, realRows)
			tree := layout.NewBSPTree()
			bounds, gap := m.GetBSPBounds(), m.separatorGap()
			prev := 0
			for _, w := range m.Windows {
				id := m.GetWindowIntID(w.ID)
				tree.InsertWindow(id, prev, layout.SplitNone, 0.5, bounds, gap)
				prev = id
			}
			m.WorkspaceTrees[m.CurrentWorkspace] = tree
			for _, w := range m.Windows {
				r := tree.ApplyLayout(bounds, gap)[m.GetWindowIntID(w.ID)]
				w.X, w.Y, w.Width, w.Height = r.X, r.Y, r.W, r.H
			}
			m.Dragging, m.DraggedWindowIndex = true, 0
			m.Windows[0].IsBeingManipulated = true
			target := m.Windows[1]
			x, y := target.X+1, target.Y+target.Height/2
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				m.UpdateDropZone(x, y+i%2)
				i++
			}
			if m.dropZone == nil || m.dropZone.Dir == layout.PreselectionNone {
				b.Fatalf("the pointer is not over an edge of a neighbour: %+v", m.dropZone)
			}
		})
	}
}
