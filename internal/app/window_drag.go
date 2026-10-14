package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// LiveWindowDrag reports the pane a window-move gesture is holding, or nil.
//
// A title-bar drag and a committed ctrl-drag both set Dragging and point
// DraggedWindowIndex at the pane, but so does a text selection, which rides
// the same flags with a pane in copy mode. What tells them apart is the mark
// beginWindowDrag puts on the pane and a selection never does: only a pane
// under the pointer's manipulation is being moved. A resize sets the same mark,
// so Resizing rules it out first.
//
// While this is non-nil the pane's rectangle belongs to the pointer. A layout
// pass leaves the rectangle alone and records the slot it would have used (see
// noteDragSlot), a state sync declines the rectangle the daemon holds for it,
// and the staleness checks read the slot in its place. Without those a peer's
// sync landing mid-drag put the pane back in its slot, and because the sync
// also carries the peer's focus, the motion that followed moved whichever pane
// the peer had focused instead: the drag ended with that pane parked at the
// pointer, overlapping the tiles.
func (m *OS) LiveWindowDrag() *terminal.Window {
	if !m.Dragging || m.Resizing || m.ScrollbarDragging {
		return nil
	}
	if m.DraggedWindowIndex < 0 || m.DraggedWindowIndex >= len(m.Windows) {
		return nil
	}
	w := m.Windows[m.DraggedWindowIndex]
	if w == nil || !w.IsBeingManipulated {
		return nil
	}
	return w
}

// noteDragSlot records the tiled slot a layout pass computed for the pane a
// drag is holding, and reports whether win is that pane. The slot is what the
// drop swaps out of or snaps back into, so a retile that runs mid-drag has to
// refresh it: a peer closing a neighbour moves every slot, and a drop that
// used the slot from the press would leave the pane at a rectangle the layout
// no longer has.
//
// The pane itself is left where the pointer has it. Placing it would also
// start a snap toward the slot, and a snap stamps its own rectangle over the
// drag on every tick until it lands.
func (m *OS) noteDragSlot(win *terminal.Window, rect layout.Rect) bool {
	if win == nil || win != m.LiveWindowDrag() {
		return false
	}
	m.TiledX, m.TiledY, m.TiledWidth, m.TiledHeight = rect.X, rect.Y, rect.W, rect.H
	return true
}

// dragSlotOf is the rectangle a staleness check should read for win: the slot
// a drag took it from while the pointer holds it, and its own rectangle
// otherwise. A pane in mid-drag is by definition somewhere the layout did not
// put it, and that is not the layout being stale.
func (m *OS) dragSlotOf(win *terminal.Window) (x, y, w, h int) {
	if win != nil && win == m.LiveWindowDrag() {
		return m.TiledX, m.TiledY, m.TiledWidth, m.TiledHeight
	}
	return win.X, win.Y, win.Width, win.Height
}

// ReturnToDragSlot puts a dragged tiled pane back in the slot the drag took it
// from, at once, so a drop that moves nothing leaves the layout as it was.
func (m *OS) ReturnToDragSlot(win *terminal.Window) {
	win.X = m.TiledX
	win.Y = m.TiledY
	win.Width = m.TiledWidth
	win.Height = m.TiledHeight
	win.Resize(m.TiledWidth, m.TiledHeight)
	win.MarkPositionDirty()
	win.InvalidateCache()
}

// CancelWindowDrag abandons a window-move gesture in flight and reports
// whether there was one. The held pane goes back to where the press took it
// from: its tiled slot, or for a floating pane the position it had. Nothing
// is dropped, swapped or split.
//
// A press of any button while a pane is held is what calls it. Each of those
// presses used to run as a fresh press on the pane under the pointer, which is
// the held pane: a right press started a resize on top of the drag, and its
// release opened the pane menu and ended the gesture there, leaving the pane
// parked at the pointer over the tiles with no slot. A middle press re-aimed
// the drag's offset, and a second left press started a new drag whose slot was
// the floating rectangle the first one had left the pane in.
func (m *OS) CancelWindowDrag() bool {
	win := m.LiveWindowDrag()
	if win == nil {
		return false
	}
	if m.AutoTiling && !win.IsFloating {
		m.ReturnToDragSlot(win)
	} else {
		// The press offset is fixed for the whole drag, so the press point
		// less the offset is where the pane was when the drag began.
		win.X, win.Y = m.DragStartX-m.DragOffsetX, m.DragStartY-m.DragOffsetY
		win.MarkPositionDirty()
		win.InvalidateCache()
	}
	m.CtrlDragging = false
	m.CtrlDragPending = false
	m.ClickToTypePending = false
	m.RightClickPending = false
	if m.CtrlDragWasTerminal {
		m.CtrlDragWasTerminal = false
		m.Mode = TerminalMode
	}
	m.endLostGesture()
	m.DragOffsetX, m.DragOffsetY = 0, 0
	m.DragStartX, m.DragStartY = 0, 0
	// The drag gave up the pane's shared-border allowance on its first
	// motion, and the retile is what gives it back, as on a drop.
	if m.AutoTiling && m.PanesBorderless() {
		m.TileAllWindows()
	}
	m.SyncStateToDaemon()
	return true
}
