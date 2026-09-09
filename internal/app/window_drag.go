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
