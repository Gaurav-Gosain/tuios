package input

import (
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// touchBorderSlop is how many cells either side of a division a finger may land
// on and still grab it.
//
// A cell on a phone is about 8px across and 18px tall, so the single column a
// mouse aims at is an 8px target where both mobile platforms ask for 44. One
// cell of slop makes a vertical division 24px wide and a horizontal one three
// rows tall, which is what a grid this coarse gives without taking a content
// column away from the panes on both sides of every division. It does not reach
// 44px and is not meant to: the finger-sized route to the same pane is the menu
// a long press opens.
//
// A pointer keeps the exact cell. It does not need the help, and the cells the
// slop claims are cells of somebody's shell.
const touchBorderSlop = 1

// borderSlop is the grab margin this session's pointer gets.
func borderSlop(o *app.OS) int {
	if o.TouchClient {
		return touchBorderSlop
	}
	return 0
}

// within reports whether v is no more than slop cells from target.
func within(v, target, slop int) bool { return abs(v-target) <= slop }

// armBorderResize starts a pane-border resize when a left press lands on a
// border cell. Reports whether it consumed the press.
//
// Tiled/BSP: the grab target is the division between two panes; dragging it
// moves the divider through the same visual-resize machinery a corner drag uses
// (AdjustTilingNeighborsVisual + deferred PTY resize).
// Floating: the grab target is the pane's own frame; the dragged edge resizes.
//
// It never fires on content (only the border/separator cells), on the sidebar
// band, or on the dock, so it is purely additive to the existing gestures.
func armBorderResize(x, y int, o *app.OS) bool {
	// Chrome owns its own cells; a pane-border grab must stay inside the content
	// region the panes actually live in.
	if o.SidebarBandContains(x, y) || o.InDockBand(y) {
		return false
	}
	// Scrolling columns drawing their own borders have the right drag for
	// their width. Under shared borders the divider between two columns is
	// drawn, and the resize pointer offered over it, so it has to be one.
	if o.AutoTiling && o.UseScrollingLayout {
		if !o.PanesBorderless() {
			return false
		}
		return armScrollDividerResize(x, y, o)
	}
	if o.AutoTiling {
		return armTiledBorderResize(x, y, o)
	}
	return armFloatingBorderResize(x, y, o)
}

// armTiledBorderResize grabs the division between two tiled panes. The pane
// whose edge was grabbed is the resize target; the layout moves the divider
// that owns that edge, so either neighbour of a division will do.
//
// Where the division is drawn depends on the panes, not on the setting. Under
// shared borders the tilers reserve the cells between neighbours (layout.
// CalculateMasterLayout, bsp.childBounds) and those cells are the divider,
// owned by neither pane. Without them the panes are edge to edge and the
// division is the border column each pane draws for itself, which is why the
// grab is measured from BorderOffset: it is the pane's own answer to whether
// it drew one.
//
// A divider cell is claimed from both sides. Most cells are past some pane's
// right or bottom edge, but not all: where a divider meets another one, the
// panes on one side stop short of the junction and only the pane across the
// line reaches it, with its left or top edge. With the master on the right or
// at the bottom that junction is the middle of the divider the user is most
// likely to grab.
func armTiledBorderResize(x, y int, o *app.OS) bool {
	contentLeft, contentTop := o.PaneLeft(), o.PaneTop()
	contentRight := contentLeft + o.PaneWidth()
	contentBottom := contentTop + o.PaneHeight()
	slop := borderSlop(o)
	gap := o.SeparatorGap()

	for i := range o.Windows {
		w := o.Windows[i]
		if w.Workspace != o.CurrentWorkspace || w.Minimized || w.IsFloating {
			continue
		}
		off := w.BorderOffset()
		inRows := y >= w.Y && y < w.Y+w.Height
		inCols := x >= w.X && x < w.X+w.Width
		// Interior divisions only. A pane edge lying on the content boundary is
		// the screen's own, with no neighbour behind it to give space to.
		switch {
		case inRows && w.X+w.Width < contentRight && onDivision(x, w.X+w.Width-off, w.X+w.Width+dividerSpan(off, gap), slop):
			beginBorderResize(o, i, app.BorderEdgeRight, w.X+w.Width-x)
		case inRows && w.X > contentLeft && off > 0 && within(x, w.X, slop):
			// The neighbour's own border is the other half of the same drawn
			// division, so grabbing it has to work too.
			beginBorderResize(o, i, app.BorderEdgeLeft, w.X-x)
		case inRows && w.X > contentLeft && off == 0 && gap > 0 && onDivision(x, w.X-gap, w.X, slop):
			beginBorderResize(o, i, app.BorderEdgeLeft, w.X-x)
		case inCols && w.Y+w.Height < contentBottom && onDivision(y, w.Y+w.Height-off, w.Y+w.Height+dividerSpan(off, gap), slop):
			beginBorderResize(o, i, app.BorderEdgeBottom, w.Y+w.Height-y)
		case inCols && w.Y > contentTop && off == 0 && gap > 0 && onDivision(y, w.Y-gap, w.Y, slop):
			// The top row of a pane drawing its own border is its title bar,
			// which stays a drag handle; the pane above owns that division.
			beginBorderResize(o, i, app.BorderEdgeTop, w.Y-y)
		default:
			continue
		}
		return true
	}
	return false
}

// armScrollDividerResize grabs the divider between two columns of the
// scrolling strip. The column on its left is the one resized: the strip has
// no other column to give the width to, and the columns after it slide along.
// Columns stacked in a column share its height evenly, so the dividers between
// them do not move.
func armScrollDividerResize(x, y int, o *app.OS) bool {
	contentRight := o.PaneLeft() + o.PaneWidth()
	gap := o.SeparatorGap()
	for i := range o.Windows {
		w := o.Windows[i]
		if w.Workspace != o.CurrentWorkspace || w.Minimized || w.IsFloating {
			continue
		}
		if y >= w.Y && y < w.Y+w.Height && w.X+w.Width < contentRight &&
			onDivision(x, w.X+w.Width, w.X+w.Width+gap, borderSlop(o)) {
			beginBorderResize(o, i, app.BorderEdgeRight, w.X+w.Width-x)
			return true
		}
	}
	return false
}

// dividerSpan is how many cells past a pane's far edge its division runs: the
// reserved cells between borderless panes, none for a pane whose own border
// column is the division.
func dividerSpan(off, gap int) int {
	if off > 0 {
		return 0
	}
	return gap
}

// onDivision reports whether v falls in the division [from, to), or within
// slop cells of it. An empty range is the single cell from.
func onDivision(v, from, to, slop int) bool {
	if to <= from {
		to = from + 1
	}
	return v >= from-slop && v < to+slop
}

// armFloatingBorderResize grabs a floating pane's own frame. The top row is the
// title bar and stays a drag handle, so only the left, right, and bottom edges
// resize.
//
// A touch client's slop reaches inwards only. The cells outside a floating pane
// are usually empty desktop and would be the cheapest ones to claim, but a tap
// on the desktop a cell away from a pane would then resize it instead of doing
// what a tap on the desktop does, and a corner outside the frame has no edge to
// pick.
func armFloatingBorderResize(x, y int, o *app.OS) bool {
	idx := findClickedWindow(x, y, o)
	if idx < 0 {
		return false
	}
	w := o.Windows[idx]
	if w.Zoomed {
		return false
	}
	slop := borderSlop(o)
	onLeft := x-w.X <= slop
	onRight := (w.X+w.Width-1)-x <= slop
	onBottom := (w.Y+w.Height-1)-y <= slop
	onTop := y == w.Y

	switch {
	case onLeft && !onTop:
		beginBorderResize(o, idx, app.BorderEdgeLeft, w.X-x)
	case onRight && !onTop:
		beginBorderResize(o, idx, app.BorderEdgeRight, w.X+w.Width-x)
	case onBottom && !onLeft && !onRight:
		beginBorderResize(o, idx, app.BorderEdgeBottom, w.Y+w.Height-y)
	default:
		return false
	}
	return true
}

// beginBorderResize arms the gesture. It reuses o.Resizing so the release path
// already flushes deferred PTY resizes, syncs the BSP tree, and pushes state to
// the daemon exactly as a corner resize does; BorderResizing selects the
// single-edge motion handler. grab is how far the edge lies from the pressed
// cell, which the motion handler keeps.
func beginBorderResize(o *app.OS, idx int, edge app.BorderResizeEdge, grab int) {
	w := o.Windows[idx]
	o.FocusWindow(idx)
	o.BeginPointerGesture()
	o.Resizing = true
	o.BorderResizing = true
	o.BorderResizeEdge = edge
	o.BorderResizeGrab = grab
	o.InteractionMode = true
	o.DraggedWindowIndex = idx
	w.IsBeingManipulated = true
	o.PreResizeState = terminal.Window{X: w.X, Y: w.Y, Width: w.Width, Height: w.Height, Z: w.Z, ID: w.ID}
}

// applyBorderResize moves the dragged edge to the pointer. Tiled panes go
// through the shared visual-resize path (which constrains split lines and defers
// the PTY resize); floating panes resize the one edge and defer the PTY resize
// to release via PendingResizes.
func applyBorderResize(o *app.OS, mx, my int) {
	idx := o.DraggedWindowIndex
	if idx < 0 || idx >= len(o.Windows) {
		return
	}
	w := o.Windows[idx]

	// The edge stays as far from the pointer as it was on the press. On a pane
	// that draws its own border the pointer sits on the last cell the pane
	// owns, so its far edge is one past the pointer, while a reserved divider
	// cell is already past the pane. Without this the pane jumps on the press,
	// before the pointer has moved at all.
	grab := o.BorderResizeGrab

	newX, newY, newW, newH := w.X, w.Y, w.Width, w.Height
	switch o.BorderResizeEdge {
	case app.BorderEdgeRight:
		newW = mx + grab - w.X
	case app.BorderEdgeLeft:
		right := w.X + w.Width
		newX = mx + grab
		newW = right - newX
	case app.BorderEdgeBottom:
		newH = my + grab - w.Y
	case app.BorderEdgeTop:
		bottom := w.Y + w.Height
		newY = my + grab
		newH = bottom - newY
	case app.BorderEdgeNone:
		return
	}

	if o.AutoTiling && o.UseScrollingLayout {
		o.ScrollingResizeColumnVisual(w, newW, false)
		return
	}
	if o.AutoTiling {
		treeInSync := o.AdjustTilingNeighborsVisual(w, newX, newY, newW, newH)
		if o.SharedBorders && !treeInSync {
			o.MarkBSPSyncPending()
		}
		return
	}

	// Floating: clamp to the minimum window size, holding the opposite edge fixed.
	if newW < config.DefaultWindowWidth {
		if o.BorderResizeEdge == app.BorderEdgeLeft {
			newX = w.X + w.Width - config.DefaultWindowWidth
		}
		newW = config.DefaultWindowWidth
	}
	if newH < config.DefaultWindowHeight {
		if o.BorderResizeEdge == app.BorderEdgeTop {
			newY = w.Y + w.Height - config.DefaultWindowHeight
		}
		newH = config.DefaultWindowHeight
	}
	w.X, w.Y = newX, newY
	w.ResizeVisual(newW, newH)
	w.MarkPositionDirty()
	o.PendingResizes[w.ID] = [2]int{newW, newH}
}
