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
	// Scrolling columns have their own width gesture; leave them alone.
	if o.AutoTiling && o.UseScrollingLayout {
		return false
	}
	if o.AutoTiling {
		return armTiledBorderResize(x, y, o)
	}
	return armFloatingBorderResize(x, y, o)
}

// armTiledBorderResize grabs the division between two tiled panes. The pane
// whose edge was grabbed is the resize target; the BSP tree moves the divider
// that owns that edge, so either neighbour of a division will do.
//
// Where the division is drawn depends on the panes, not on the setting. Under
// shared borders the tilers reserve a cell between neighbours (layout.
// CalculateTilingLayout, bsp.childBounds) and that cell is the divider, owned by
// neither pane. Without them the panes are edge to edge and the division is the
// border column each pane draws for itself, which is why the grab is measured
// from BorderOffset: it is the pane's own answer to whether it drew one.
func armTiledBorderResize(x, y int, o *app.OS) bool {
	contentLeft, contentTop := o.GetLeftMargin(), o.GetTopMargin()
	contentRight := contentLeft + o.GetContentWidth()
	contentBottom := contentTop + o.GetUsableHeight()
	slop := borderSlop(o)

	for i := range o.Windows {
		w := o.Windows[i]
		if w.Workspace != o.CurrentWorkspace || w.Minimized || w.IsFloating {
			continue
		}
		off := w.BorderOffset()
		// Interior divisions only. A pane edge lying on the content boundary is
		// the screen's own, with no neighbour behind it to give space to.
		switch {
		case y >= w.Y && y < w.Y+w.Height && within(x, w.X+w.Width-off, slop) && w.X+w.Width < contentRight:
			beginBorderResize(o, i, app.BorderEdgeRight)
		case y >= w.Y && y < w.Y+w.Height && off > 0 && within(x, w.X, slop) && w.X > contentLeft:
			// The neighbour's own border is the other half of the same drawn
			// division, so grabbing it has to work too.
			beginBorderResize(o, i, app.BorderEdgeLeft)
		case x >= w.X && x < w.X+w.Width && within(y, w.Y+w.Height-off, slop) && w.Y+w.Height < contentBottom:
			beginBorderResize(o, i, app.BorderEdgeBottom)
		default:
			continue
		}
		return true
	}
	return false
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
		beginBorderResize(o, idx, app.BorderEdgeLeft)
	case onRight && !onTop:
		beginBorderResize(o, idx, app.BorderEdgeRight)
	case onBottom && !onLeft && !onRight:
		beginBorderResize(o, idx, app.BorderEdgeBottom)
	default:
		return false
	}
	return true
}

// beginBorderResize arms the gesture. It reuses o.Resizing so the release path
// already flushes deferred PTY resizes, syncs the BSP tree, and pushes state to
// the daemon exactly as a corner resize does; BorderResizing selects the
// single-edge motion handler.
func beginBorderResize(o *app.OS, idx int, edge app.BorderResizeEdge) {
	w := o.Windows[idx]
	o.FocusWindow(idx)
	o.BeginPointerGesture()
	o.Resizing = true
	o.BorderResizing = true
	o.BorderResizeEdge = edge
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

	// The same allowance the arming used, read from the same place: on a pane
	// that draws its own border the pointer sits on the last cell the pane owns,
	// so its far edge is one past the pointer, while a reserved separator cell is
	// already past the pane. Without this the pane jumps a cell on the press,
	// before the pointer has moved at all.
	off := w.BorderOffset()

	newX, newY, newW, newH := w.X, w.Y, w.Width, w.Height
	switch o.BorderResizeEdge {
	case app.BorderEdgeRight:
		newW = mx - w.X + off
	case app.BorderEdgeLeft:
		right := w.X + w.Width
		newX = mx
		newW = right - mx
	case app.BorderEdgeBottom:
		newH = my - w.Y + off
	case app.BorderEdgeTop:
		bottom := w.Y + w.Height
		newY = my
		newH = bottom - my
	case app.BorderEdgeNone:
		return
	}

	if o.AutoTiling && !o.UseScrollingLayout {
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
