package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Dropping a dragged pane on another one in BSP. The middle of the target swaps
// the two, and each edge splits the target, the dragged pane taking the half
// toward that edge while its old neighbour fills the slot it left.
//
// What is highlighted during the drag and what the release does are both read
// from TiledDropAt, at the pointer, so the two cannot disagree.

// Drop is what releasing the held pane at a point would do.
type Drop struct {
	Target *terminal.Window
	// Dir is the edge of Target the drop splits toward. PreselectionNone is
	// its middle, which swaps.
	Dir layout.PreselectionDir
	// Land is the rectangle the held pane would end up in, in the layout
	// frame. An edge drop that would leave the tree as it is lands the pane
	// back in its own slot.
	Land layout.Rect
}

// DropTargetAt is the index of the pane a drop at (x, y) lands on: a tiled pane
// of this workspace other than the one being dragged, or -1.
func (m *OS) DropTargetAt(x, y int) int {
	for i, w := range m.Windows {
		if i == m.DraggedWindowIndex || w.Minimized || w.IsFloating || w.Workspace != m.CurrentWorkspace {
			continue
		}
		if x >= w.X && x < w.X+w.Width && y >= w.Y && y < w.Y+w.Height {
			return i
		}
	}
	return -1
}

// TiledDropAt is the drop a release at (x, y) would make in BSP. It reports
// false when edge drops are off and the release keeps the plain swap: another
// layout is on screen, nothing tiled is held or under the pointer, a pane is
// zoomed, or a scratch group is shown.
//
// The other two layouts place panes by rules of their own, so a split there
// would be undone by the next retile. A zoom lays panes out in a box of its
// own, and the scratch group in its own tree, so a landing rectangle worked
// out against the screen would be the wrong one.
func (m *OS) TiledDropAt(x, y int) (Drop, bool) {
	held := m.LiveWindowDrag()
	if held == nil || !m.AutoTiling || !m.UseBSPLayout || m.UseScrollingLayout || m.zoomedWindow() != nil {
		return Drop{}, false
	}
	if _, shown := m.ScratchBox(); shown {
		return Drop{}, false
	}
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	heldID := m.GetWindowIntID(held.ID)
	idx := m.DropTargetAt(x, y)
	if tree == nil || idx < 0 || !tree.HasWindow(heldID) {
		return Drop{}, false
	}
	target := m.Windows[idx]
	targetID := m.GetWindowIntID(target.ID)
	if !tree.HasWindow(targetID) {
		return Drop{}, false
	}

	rect := layout.Rect{X: target.X, Y: target.Y, W: target.Width, H: target.Height}
	drop := Drop{Target: target, Dir: dropEdge(rect, x, y, m.separatorGap()), Land: rect}
	if drop.Dir == layout.PreselectionNone {
		return drop, true
	}
	// Worked out on a copy, because removing the held pane first can grow the
	// target: a drop on its own neighbour splits the space both of them had.
	moved := tree.Serialize().Deserialize()
	if !moved.MoveWindow(heldID, targetID, drop.Dir) {
		drop.Land = layout.Rect{X: m.TiledX, Y: m.TiledY, W: m.TiledWidth, H: m.TiledHeight}
		return drop, true
	}
	drop.Land = moved.ApplyLayout(m.GetBSPBounds(), m.separatorGap())[heldID]
	return drop, true
}

// dropEdge is the edge of r a drop at (x, y) splits toward, or
// PreselectionNone for the middle of r. An edge's zone is the outer quarter of
// r on that side. It is there only when r can hold two panes of the minimum
// size across that axis, so a drop never makes a pane smaller than the layout
// allows. In a corner the edge the pointer is deeper into wins, and a tie goes
// to the side.
func dropEdge(r layout.Rect, x, y, gap int) layout.PreselectionDir {
	edge, depth := layout.PreselectionNone, 1.0
	consider := func(dir layout.PreselectionDir, from, zone int) {
		if d := float64(from) / float64(zone); d < depth {
			edge, depth = dir, d
		}
	}
	if r.W >= 2*config.DefaultWindowWidth+gap {
		zone := max(1, r.W/4)
		consider(layout.PreselectionLeft, x-r.X, zone)
		consider(layout.PreselectionRight, r.X+r.W-1-x, zone)
	}
	if r.H >= 2*config.DefaultWindowHeight+gap {
		zone := max(1, r.H/4)
		consider(layout.PreselectionUp, y-r.Y, zone)
		consider(layout.PreselectionDown, r.Y+r.H-1-y, zone)
	}
	return edge
}

// UpdateDropZone records the drop under the pointer for the highlight. The
// motion path calls it on every step of a drag.
func (m *OS) UpdateDropZone(x, y int) {
	if drop, ok := m.TiledDropAt(x, y); ok {
		m.dropZone = &drop
		return
	}
	m.dropZone = nil
}

// ClearDropZone forgets the last drag's drop. A drag's press, its release and
// a lost release each call it, so a highlight never outlives the gesture it was
// worked out for and a closed pane is not kept alive as a drop's target. The
// render pass draws nothing without a drag besides.
func (m *OS) ClearDropZone() {
	m.dropZone = nil
}

// MoveWindowIntoSplit makes an edge drop: dragged leaves its slot and splits
// target toward dir, taking the new half. It reports false, changing nothing,
// when the tree would come out as it went in.
func (m *OS) MoveWindowIntoSplit(dragged, target *terminal.Window, dir layout.PreselectionDir) bool {
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil {
		return false
	}
	m.requireRealLayout()
	if !tree.MoveWindow(m.GetWindowIntID(dragged.ID), m.GetWindowIntID(target.ID), dir) {
		return false
	}
	// The drop hands the pane back to the layout. With the manipulation mark
	// still on it the retile would leave it at the pointer and only note its
	// slot (see LiveWindowDrag).
	dragged.IsBeingManipulated = false
	m.ApplyBSPLayout()
	return true
}
