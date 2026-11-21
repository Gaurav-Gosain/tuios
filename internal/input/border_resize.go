package input

import (
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

const touchBorderSlop = 1

func borderSlop(o *app.OS) int {
	if o.TouchClient {
		return touchBorderSlop
	}
	return 0
}

func within(v, target, slop int) bool { return abs(v-target) <= slop }

func armBorderResize(x, y int, o *app.OS) bool {
	if o.SidebarBandContains(x, y) || o.InDockBand(y) {
		return false
	}
	if o.AutoTiling {
		if idx := findClickedWindow(x, y, o); idx >= 0 {
			if w := o.Windows[idx]; w.IsFloating && !w.IsPopup && !w.IsScratch {
				return armFloatingBorderResize(x, y, o)
			}
		}
	}
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

func armTiledBorderResize(x, y int, o *app.OS) bool {
	if paneOver(x, y, o) {
		return false
	}
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
		switch {
		case inRows && w.X+w.Width < contentRight && onDivision(x, w.X+w.Width-off, w.X+w.Width+dividerSpan(off, gap), slop):
			beginBorderResize(o, i, app.BorderEdgeRight, w.X+w.Width-x)
		case inRows && w.X > contentLeft && off > 0 && within(x, w.X, slop):
			beginBorderResize(o, i, app.BorderEdgeLeft, w.X-x)
		case inRows && w.X > contentLeft && off == 0 && gap > 0 && onDivision(x, w.X-gap, w.X, slop):
			beginBorderResize(o, i, app.BorderEdgeLeft, w.X-x)
		case inCols && w.Y+w.Height < contentBottom && onDivision(y, w.Y+w.Height-off, w.Y+w.Height+dividerSpan(off, gap), slop):
			beginBorderResize(o, i, app.BorderEdgeBottom, w.Y+w.Height-y)
		case inCols && w.Y > contentTop && off == 0 && gap > 0 && onDivision(y, w.Y-gap, w.Y, slop):
			beginBorderResize(o, i, app.BorderEdgeTop, w.Y-y)
		default:
			continue
		}
		return true
	}
	return false
}

func paneOver(x, y int, o *app.OS) bool {
	idx := findClickedWindow(x, y, o)
	if idx < 0 {
		return false
	}
	w := o.Windows[idx]
	return w.IsFloating || w.Zoomed
}

func armScrollDividerResize(x, y int, o *app.OS) bool {
	if findClickedWindow(x, y, o) >= 0 {
		return false
	}
	contentRight := o.PaneLeft() + o.PaneWidth()
	contentTop := o.PaneTop()
	if y < contentTop || y >= contentTop+o.PaneHeight() {
		return false
	}
	o.CompleteAllAnimations()
	gap := o.SeparatorGap()
	slop := borderSlop(o)
	tiled := func(w *terminal.Window) bool {
		return w.Workspace == o.CurrentWorkspace && !w.Minimized && !w.IsFloating
	}
	found := -1
	for i, w := range o.Windows {
		right := w.X + w.Width
		if !tiled(w) || right >= contentRight || !onDivision(x, right, right+gap, slop) {
			continue
		}
		neighbour := false
		for _, n := range o.Windows {
			if tiled(n) && n.X == right+gap {
				neighbour = true
				break
			}
		}
		if !neighbour {
			continue
		}
		if found < 0 || (y >= w.Y && y < w.Y+w.Height) {
			found = i
		}
	}
	if found < 0 {
		return false
	}
	w := o.Windows[found]
	beginBorderResizeAt(o, found, app.BorderEdgeRight, w.X+w.Width-x, false)
	return true
}

func dividerSpan(off, gap int) int {
	if off > 0 {
		return 0
	}
	return gap
}

func onDivision(v, from, to, slop int) bool {
	if to <= from {
		to = from + 1
	}
	return v >= from-slop && v < to+slop
}

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

func beginBorderResize(o *app.OS, idx int, edge app.BorderResizeEdge, grab int) {
	beginBorderResizeAt(o, idx, edge, grab, true)
}

func beginBorderResizeAt(o *app.OS, idx int, edge app.BorderResizeEdge, grab int, focus bool) {
	w := o.Windows[idx]
	if focus {
		o.FocusWindow(idx)
	}
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

func applyBorderResize(o *app.OS, mx, my int) {
	idx := o.DraggedWindowIndex
	if idx < 0 || idx >= len(o.Windows) {
		return
	}
	w := o.Windows[idx]

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

	tiled := o.ResizesWithTiling(w)
	if tiled && o.UseScrollingLayout {
		o.ScrollingResizeColumnVisual(w, mx+grab-o.PreResizeState.X, false)
		return
	}
	if tiled {
		treeInSync := o.AdjustTilingNeighborsVisual(w, newX, newY, newW, newH)
		if o.SharedBorders && !treeInSync {
			o.MarkBSPSyncPending()
		}
		return
	}

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
