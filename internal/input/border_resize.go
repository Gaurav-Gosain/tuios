package input

import (
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// armBorderResize starts a pane-border resize when a left press lands on a
// border cell. Reports whether it consumed the press.
//
// Tiled/BSP: the grab target is the reserved separator cell between two panes;
// dragging it moves the shared divider through the same visual-resize machinery
// a corner drag uses (AdjustTilingNeighborsVisual + deferred PTY resize).
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
		// Only shared borders reserve a separator cell to grab; without them the
		// panes are edge-to-edge and every column is content.
		if !config.SharedBorders {
			return false
		}
		return armTiledBorderResize(x, y, o)
	}
	return armFloatingBorderResize(x, y, o)
}

// armTiledBorderResize grabs the shared divider between two tiled panes. The
// near pane (left of a vertical divider, above a horizontal one) is the resize
// target; its far edge is the divider the BSP/tiling path will move.
func armTiledBorderResize(x, y int, o *app.OS) bool {
	contentRight := o.GetLeftMargin() + o.GetContentWidth()
	contentBottom := o.GetTopMargin() + o.GetUsableHeight()

	for i := range o.Windows {
		w := o.Windows[i]
		if w.Workspace != o.CurrentWorkspace || w.Minimized || w.IsFloating {
			continue
		}
		// Vertical divider: the separator column sits one past this pane's right
		// edge. Interior only; the outermost right edge is the screen boundary.
		if x == w.X+w.Width && x < contentRight && y >= w.Y && y < w.Y+w.Height {
			beginBorderResize(o, i, app.BorderEdgeRight)
			return true
		}
		// Horizontal divider: the separator row sits one past this pane's bottom.
		if y == w.Y+w.Height && y < contentBottom && x >= w.X && x < w.X+w.Width {
			beginBorderResize(o, i, app.BorderEdgeBottom)
			return true
		}
	}
	return false
}

// armFloatingBorderResize grabs a floating pane's own frame. The top row is the
// title bar and stays a drag handle, so only the left, right, and bottom edges
// resize.
func armFloatingBorderResize(x, y int, o *app.OS) bool {
	idx := findClickedWindow(x, y, o)
	if idx < 0 {
		return false
	}
	w := o.Windows[idx]
	if w.Zoomed {
		return false
	}
	onLeft := x == w.X
	onRight := x == w.X+w.Width-1
	onBottom := y == w.Y+w.Height-1
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
	o.BeginResizeMode()
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

	newX, newY, newW, newH := w.X, w.Y, w.Width, w.Height
	switch o.BorderResizeEdge {
	case app.BorderEdgeRight:
		newW = mx - w.X
	case app.BorderEdgeLeft:
		right := w.X + w.Width
		newX = mx
		newW = right - mx
	case app.BorderEdgeBottom:
		newH = my - w.Y
	case app.BorderEdgeTop:
		bottom := w.Y + w.Height
		newY = my
		newH = bottom - my
	case app.BorderEdgeNone:
		return
	}

	if o.AutoTiling && !o.UseScrollingLayout {
		treeInSync := o.AdjustTilingNeighborsVisual(w, newX, newY, newW, newH)
		if config.SharedBorders && !treeInSync {
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
