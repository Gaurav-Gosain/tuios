package app

import (
	"slices"

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

func (m *OS) noteDragSlot(win *terminal.Window, rect layout.Rect) bool {
	if win == nil || win != m.LiveWindowDrag() {
		return false
	}
	m.TiledX, m.TiledY, m.TiledWidth, m.TiledHeight = rect.X, rect.Y, rect.W, rect.H
	return true
}

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
		win.X, win.Y = m.DragStartX-m.DragOffsetX, m.DragStartY-m.DragOffsetY
		win.MarkPositionDirty()
		win.InvalidateCache()
	}
	m.CtrlDragging = false
	m.CtrlDragPending = false
	m.ClickToTypePending = false
	m.RightClickPending = false
	m.EndCtrlDrag()
	m.endLostGesture()
	m.DragOffsetX, m.DragOffsetY = 0, 0
	m.DragStartX, m.DragStartY = 0, 0
	if m.AutoTiling && m.PanesBorderless() {
		m.TileAllWindows()
	}
	m.SyncStateToDaemon()
	return true
}

// CancelPointerGesture abandons whatever gesture the pointer holds a pane by,
// a move or a resize, and reports whether there was one. A press of any button
// while a pane is held calls it, so the pointer holds one gesture at a time.
func (m *OS) CancelPointerGesture() bool {
	return m.CancelWindowDrag() || m.CancelWindowResize()
}

// CancelWindowResize abandons a resize in flight, by a corner (the right-button
// drag) or by a border, and reports whether there was one. The held pane goes
// back to the rectangle the press found it at, the deferred PTY resizes are
// dropped, and a tiled layout is laid again from the tree, which a resize only
// changes on its release. Nothing is committed.
//
// Before it, a left press during a resize started a move of the pane under the
// pointer on top of the resize. With Dragging and Resizing both set the motion
// moved that pane, and the release took neither drop path, because the tiling
// drop rules out a resize, and ended as a resize: the moved pane stayed where
// the pointer left it, over the tiles, at a rectangle the tree does not hold.
func (m *OS) CancelWindowResize() bool {
	if !m.Resizing || m.Dragging || m.ScrollbarDragging {
		return false
	}
	if i := m.DraggedWindowIndex; i >= 0 && i < len(m.Windows) {
		win, pre := m.Windows[i], &m.PreResizeState
		aw, ah := win.AnnouncedSize()
		padW, padH := win.Width-win.ContentWidth(), win.Height-win.ContentHeight()
		if win.ID == pre.ID && !win.Zoomed && pre.Width-padW == aw && pre.Height-padH == ah {
			win.X, win.Y = pre.X, pre.Y
			win.ResizeVisual(pre.Width, pre.Height)
			win.MarkPositionDirty()
			win.InvalidateCache()
		}
	}
	pending := m.PendingResizes
	m.PendingResizes = make(map[string][2]int)
	m.Resizing = false
	m.ClickToTypePending = false
	m.endLostGesture()
	m.ResizeStartX, m.ResizeStartY = 0, 0
	if m.AutoTiling && !m.UseScrollingLayout {
		m.TileAllWindows()
	}
	m.tellKeptDeferredSizes(pending)
	m.SyncStateToDaemon()
	return true
}

func (m *OS) tellKeptDeferredSizes(pending map[string][2]int) {
	for _, win := range m.Windows {
		if dims, ok := pending[win.ID]; ok && win.Width == dims[0] && win.Height == dims[1] {
			m.resizeNow(win, dims[0], dims[1])
		}
	}
}

type heldPaneIDs struct {
	drag, scrollbar string
}

func (m *OS) heldPanes() heldPaneIDs {
	var h heldPaneIDs
	gesture := m.Dragging || m.Resizing || m.BorderResizing
	if i := m.DraggedWindowIndex; gesture && i >= 0 && i < len(m.Windows) {
		h.drag = m.Windows[i].ID
	}
	if i := m.ScrollbarDragWindowIndex; m.ScrollbarDragging && i >= 0 && i < len(m.Windows) {
		h.scrollbar = m.Windows[i].ID
	}
	return h
}

func (m *OS) followHeldPanes(h heldPaneIDs) bool {
	ok := true
	follow := func(id string, index *int) {
		if id == "" {
			return
		}
		*index = slices.IndexFunc(m.Windows, func(w *terminal.Window) bool { return w.ID == id })
		if *index < 0 {
			ok = false
		}
	}
	follow(h.drag, &m.DraggedWindowIndex)
	follow(h.scrollbar, &m.ScrollbarDragWindowIndex)
	return ok
}
