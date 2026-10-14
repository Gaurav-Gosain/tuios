package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

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
	return m.tiledDropAt(x, y, nil)
}

// tiledDropAt is TiledDropAt, taking the landing rectangle from last when it
// was worked out for the same target and edge. Between retiles the rectangle
// only changes when one of those does, and a retile refreshes it without last
// (see refreshDropZone). Working it out copies and lays out the whole tree,
// which a drag would otherwise pay on every motion event.
func (m *OS) tiledDropAt(x, y int, last *Drop) (Drop, bool) {
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
	drop := Drop{Target: target, Dir: dropEdge(rect, x, y, m.separatorGap(), tree.NewSplitRatio()), Land: rect}
	if drop.Dir == layout.PreselectionNone {
		return drop, true
	}
	if last != nil && last.Target == target && last.Dir == drop.Dir {
		drop.Land = last.Land
		return drop, true
	}
	// Worked out on a copy, because removing the held pane first can grow the
	// target: a drop on its own neighbour splits the space both of them had.
	moved := tree.Serialize().Deserialize()
	if !moved.MoveWindow(heldID, targetID, drop.Dir) {
		drop.Land = layout.Rect{X: m.TiledX, Y: m.TiledY, W: m.TiledWidth, H: m.TiledHeight}
		return drop, true
	}
	if m.dropLayout == nil {
		m.dropLayout = make(map[int]layout.Rect)
	}
	drop.Land = moved.ApplyLayoutInto(m.GetBSPBounds(), m.dropLayout, m.separatorGap())[heldID]
	return drop, true
}

// dropEdge is the edge of r a drop at (x, y) splits toward, or
// PreselectionNone for the middle of r. An edge's zone is the outer quarter of
// r on that side. It is there only when splitting r at ratio, the ratio the
// tree gives a new split, leaves both parts at least the minimum pane size
// across that axis, so a drop never makes a pane smaller than the layout
// allows. In a corner the edge the pointer is deeper into wins, and a tie goes
// to the side.
func dropEdge(r layout.Rect, x, y, gap int, ratio float64) layout.PreselectionDir {
	edge, depth := layout.PreselectionNone, 1.0
	consider := func(dir layout.PreselectionDir, from, zone int) {
		if d := float64(from) / float64(zone); d < depth {
			edge, depth = dir, d
		}
	}
	if splitHolds(r.W, gap, ratio, config.DefaultWindowWidth) {
		zone := max(1, r.W/4)
		consider(layout.PreselectionLeft, x-r.X, zone)
		consider(layout.PreselectionRight, r.X+r.W-1-x, zone)
	}
	if splitHolds(r.H, gap, ratio, config.DefaultWindowHeight) {
		zone := max(1, r.H/4)
		consider(layout.PreselectionUp, y-r.Y, zone)
		consider(layout.PreselectionDown, r.Y+r.H-1-y, zone)
	}
	return edge
}

// splitHolds reports whether a length split at ratio, the way the layout
// splits it (see layout.childBounds), leaves both parts at least least long.
// An even split is not assumed: a tree whose new splits start at 0.3 gives
// the near part less than half, and a check at 0.5 would let it fall below the
// minimum.
func splitHolds(length, gap int, ratio float64, least int) bool {
	near := int(float64(length) * ratio)
	return near >= least && length-near-gap >= least
}

// UpdateDropZone records the drop under the pointer for the highlight. The
// motion path calls it on every step of a drag, so a pointer that stays over
// one edge reuses the landing rectangle it already has and arms nothing.
//
// The highlight waits appearance.drop_preview_delay on a zone before it shows,
// so a pane carried across the screen does not flash a zone over every pane it
// passes. Entering a zone arms one timer for it; leaving the zone, for another
// or for none, cancels it by moving the generation on, so a zone left before
// its delay never shows. Nothing ticks while the pointer rests. The returned
// command is that timer, or nil.
//
// With the preview off nothing is recorded. The release asks TiledDropAt on
// its own, so the drop is the same either way.
func (m *OS) UpdateDropZone(x, y int) tea.Cmd {
	if m.Settings.DropPreview == config.DropPreviewOff {
		m.ClearDropZone()
		return nil
	}
	m.dropZoneX, m.dropZoneY = x, y
	drop, ok := m.tiledDropAt(x, y, m.dropZone)
	if !ok {
		m.ClearDropZone()
		return nil
	}
	if m.sameDropZone(drop) {
		return nil
	}
	if m.dropZone == nil {
		m.dropZone = new(Drop)
	}
	*m.dropZone = drop
	m.dropZoneGen++
	m.dropZoneShown = false
	delay := m.Settings.DropPreviewDelay
	if delay <= 0 {
		m.dropZoneShown = true
		return nil
	}
	gen := m.dropZoneGen
	return tea.Tick(delay, func(time.Time) tea.Msg { return dropZoneDueMsg{gen: gen} })
}

// sameDropZone takes drop's landing rectangle when drop is the zone already
// recorded, the same target and edge, and reports whether it was. The zone's
// delay and whether it shows carry over untouched.
func (m *OS) sameDropZone(drop Drop) bool {
	last := m.dropZone
	if last == nil || last.Target != drop.Target || last.Dir != drop.Dir {
		return false
	}
	*last = drop
	return true
}

// refreshDropZone works the highlight out again where the pointer last was,
// without the cached landing rectangle. ApplyBSPLayout calls it: a retile
// under a held drag (a pane exiting, a peer's change, a new box) moves where
// an edge drop lands while the target and the edge stay the same, and a
// pointer held still would otherwise keep showing the old rectangle. The
// motion path does not retile, so the cache still serves every motion event.
//
// The same zone keeps its delay and whether it shows. A retile that puts the
// pointer over another zone, or over none, clears it: a retile has no command
// to arm a delay with, and showing the new zone at once would skip the delay.
// The next motion finds no zone and arms the one under it as any entry does.
func (m *OS) refreshDropZone() {
	if m.dropZone == nil {
		return
	}
	if drop, ok := m.tiledDropAt(m.dropZoneX, m.dropZoneY, nil); ok && m.sameDropZone(drop) {
		return
	}
	m.ClearDropZone()
}

// dropZoneDueMsg is the delay of the zone armed as gen running out.
type dropZoneDueMsg struct{ gen uint64 }

// showDropZone draws the zone the pointer is on, when the delay that ran out
// was armed for it. A delay armed for a zone the pointer has since left, or
// for a drag that has ended, finds the generation moved on and does nothing.
func (m *OS) showDropZone(msg dropZoneDueMsg) {
	if msg.gen != m.dropZoneGen || m.dropZone == nil {
		return
	}
	m.dropZoneShown = true
	m.renderSkipped = false
}

// ClearDropZone forgets the last drag's drop and cancels its pending delay. A
// drag's press, its release and a lost release each call it, so a highlight
// never outlives the gesture it was worked out for and a closed pane is not
// kept alive as a drop's target. The render pass draws nothing without a drag
// besides.
func (m *OS) ClearDropZone() {
	if m.dropZone != nil {
		m.dropZoneGen++
	}
	m.dropZone = nil
	m.dropZoneShown = false
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
