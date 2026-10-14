package app

import (
	"fmt"
	"image/color"
	"slices"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
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

// BenchmarkDropZoneRender is the drop preview's pass over one frame: the
// landing rectangle of a left-edge drop on a busy pane, every cell coloured
// the way a prompt and an ls listing colour them. Each step puts the zone's
// cells back first, so every frame washes the same text the screen holds.
func BenchmarkDropZoneRender(b *testing.B) {
	for _, mode := range []string{config.DropPreviewFill, config.DropPreviewOutline} {
		b.Run(mode, func(b *testing.B) {
			m := tiledKeystrokeOS(b, 2, realCols, realRows)
			m.Settings.DropPreview = mode
			m.Settings.DropPreviewDelay = 0
			m.Dragging, m.DraggedWindowIndex = true, 0
			m.Windows[0].IsBeingManipulated = true
			target := m.Windows[1]
			m.UpdateDropZone(target.X+1, target.Y+target.Height/2)
			if !m.dropZoneShown {
				b.Fatalf("the zone is not shown: %+v", m.dropZone)
			}
			canvas := &frameCanvas{Buffer: *uv.NewBuffer(realCols, realRows)}
			inks := []color.Color{
				nil,
				color.RGBA{0x7a, 0xa2, 0xf7, 0xff},
				color.RGBA{0xf7, 0x76, 0x8e, 0xff},
				color.RGBA{0x9e, 0xce, 0x6a, 0xff},
				color.RGBA{0xe0, 0xaf, 0x68, 0xff},
				ansi.BasicColor(4),
				ansi.IndexedColor(244),
			}
			grounds := []color.Color{nil, nil, nil, color.RGBA{0x41, 0x48, 0x68, 0xff}}
			for y, row := range canvas.Lines {
				for x := range row {
					row[x] = uv.Cell{Content: "x", Width: 1, Style: uv.Style{
						Fg: inks[(x/3+y)%len(inks)],
						Bg: grounds[(x/7+y)%len(grounds)],
					}}
				}
			}
			saved := make([]uv.Line, len(canvas.Lines))
			for y, row := range canvas.Lines {
				saved[y] = slices.Clone(row)
			}
			id := m.Windows[0].ID
			land := m.dropZone.Land
			b.ReportAllocs()
			for b.Loop() {
				for y := land.Y; y < land.Y+land.H; y++ {
					copy(canvas.Lines[y][land.X:land.X+land.W], saved[y][land.X:land.X+land.W])
				}
				m.applyDropZone(canvas, id)
			}
		})
	}
}
