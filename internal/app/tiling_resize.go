package app

import (
	"math"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// ResizeFocusedWindowHeight resizes the focused window's height by moving the BOTTOM edge
// delta is in pixels (positive = grow, negative = shrink)
func (m *OS) ResizeFocusedWindowHeight(deltaPixels int) {
	if !m.AutoTiling || m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}

	focusedWindow := m.Windows[m.FocusedWindow]
	if focusedWindow.Workspace != m.CurrentWorkspace || focusedWindow.Minimized {
		return
	}

	contentBottom := m.PaneTop() + m.PaneHeight()
	atBottomEdge := (focusedWindow.Y + focusedWindow.Height) >= (contentBottom - edgeTolerance)
	if atBottomEdge {
		if focusedWindow.Y <= m.PaneTop()+edgeTolerance {
			return
		}
		m.AdjustTilingNeighbors(focusedWindow, focusedWindow.X, focusedWindow.Y-deltaPixels, focusedWindow.Width, focusedWindow.Height+deltaPixels)
		return
	}

	newX := focusedWindow.X
	newY := focusedWindow.Y
	newWidth := focusedWindow.Width
	newHeight := focusedWindow.Height + deltaPixels

	m.AdjustTilingNeighbors(focusedWindow, newX, newY, newWidth, newHeight)
}

// ResizeFocusedWindowWidth resizes the focused window's width by moving the RIGHT edge
// delta is in pixels (positive = grow right, negative = shrink left)
func (m *OS) ResizeFocusedWindowWidth(deltaPixels int) {
	if !m.AutoTiling || m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}

	focusedWindow := m.Windows[m.FocusedWindow]
	if focusedWindow.Workspace != m.CurrentWorkspace || focusedWindow.Minimized {
		return
	}

	if m.UseScrollingLayout {
		m.scrollingResizeColumn(deltaPixels)
		return
	}

	contentRight := m.PaneLeft() + m.PaneWidth()
	atRightEdge := (focusedWindow.X + focusedWindow.Width) >= (contentRight - edgeTolerance)
	if atRightEdge {
		if focusedWindow.X <= m.PaneLeft()+edgeTolerance {
			return
		}
		m.AdjustTilingNeighbors(focusedWindow, focusedWindow.X-deltaPixels, focusedWindow.Y, focusedWindow.Width+deltaPixels, focusedWindow.Height)
		return
	}

	newX := focusedWindow.X
	newY := focusedWindow.Y
	newWidth := focusedWindow.Width + deltaPixels
	newHeight := focusedWindow.Height

	m.AdjustTilingNeighbors(focusedWindow, newX, newY, newWidth, newHeight)
}

// ResizeFocusedWindowWidthLeft resizes the focused window's width by moving the LEFT edge
// delta is in pixels (positive = shrink from left, negative = grow from left)
func (m *OS) ResizeFocusedWindowWidthLeft(deltaPixels int) {
	if !m.AutoTiling || m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}

	focusedWindow := m.Windows[m.FocusedWindow]
	if focusedWindow.Workspace != m.CurrentWorkspace || focusedWindow.Minimized {
		return
	}

	if m.UseScrollingLayout {
		m.scrollingResizeColumn(-deltaPixels)
		return
	}

	atLeftEdge := focusedWindow.X <= m.PaneLeft()+edgeTolerance
	if atLeftEdge {
		return
	}

	newX := focusedWindow.X + deltaPixels
	newY := focusedWindow.Y
	newWidth := focusedWindow.Width - deltaPixels
	newHeight := focusedWindow.Height

	m.AdjustTilingNeighbors(focusedWindow, newX, newY, newWidth, newHeight)
}

// ResizeFocusedWindowHeightTop resizes the focused window's height by moving the TOP edge
// delta is in pixels (positive = shrink from top, negative = grow from top)
func (m *OS) ResizeFocusedWindowHeightTop(deltaPixels int) {
	if !m.AutoTiling || m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}

	focusedWindow := m.Windows[m.FocusedWindow]
	if focusedWindow.Workspace != m.CurrentWorkspace || focusedWindow.Minimized {
		return
	}

	atTopEdge := focusedWindow.Y <= m.PaneTop()+edgeTolerance
	if atTopEdge {
		return
	}

	newX := focusedWindow.X
	newY := focusedWindow.Y + deltaPixels
	newWidth := focusedWindow.Width
	newHeight := focusedWindow.Height - deltaPixels

	m.AdjustTilingNeighbors(focusedWindow, newX, newY, newWidth, newHeight)
}

// SetFocusedWindowWidthPercent resizes the focused window so its width is pct
// percent of the content region, driving the same edge logic the plain width
// keys use: a window sized by percentage rather than by a fixed number of
// columns. pct is accepted in 10..90, matching the resize_width_N actions;
// anything outside that range is a no-op. Popups are skipped: they already
// carry their own percentage model (PopupWidth), and applyPopupRects would
// re-centre them on the next tile anyway.
func (m *OS) SetFocusedWindowWidthPercent(pct int) {
	if pct < 10 || pct > 90 || m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}
	w := m.Windows[m.FocusedWindow]
	if w.Workspace != m.CurrentWorkspace || w.Minimized || w.IsPopup {
		return
	}
	target := m.PaneWidth() * pct / 100
	if delta := target - w.Width; delta != 0 {
		m.ResizeFocusedWindowWidth(delta)
	}
}

// SetFocusedWindowHeightPercent resizes the focused window so its height is
// pct percent of the usable height, the vertical counterpart of
// SetFocusedWindowWidthPercent. pct is accepted in 10..90, matching the
// resize_height_N actions; anything outside that range is a no-op. Popups are
// skipped for the same reason as the width path.
func (m *OS) SetFocusedWindowHeightPercent(pct int) {
	if pct < 10 || pct > 90 || m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}
	w := m.Windows[m.FocusedWindow]
	if w.Workspace != m.CurrentWorkspace || w.Minimized || w.IsPopup {
		return
	}
	target := m.PaneHeight() * pct / 100
	if delta := target - w.Height; delta != 0 {
		m.ResizeFocusedWindowHeight(delta)
	}
}

// ResizesWithTiling reports whether a resize of w moves the tiling's split
// lines. A floating pane sits over the tiling and owns no split, so it resizes
// alone, the way every pane does with tiling off. Asking AutoTiling alone sent a
// floating pane through the split scan: its edges on the screen boundary were
// held still, and the tiled panes that shared a line with the edge it moved
// were moved with it.
func (m *OS) ResizesWithTiling(w *terminal.Window) bool {
	return m.AutoTiling && w != nil && !w.IsFloating
}

type resizeOp func(m *OS, win *terminal.Window, width, height int)

func resizeImmediate(m *OS, win *terminal.Window, width, height int) {
	m.resizeNow(win, width, height)
}

func resizeVisual(m *OS, win *terminal.Window, width, height int) {
	win.ResizeVisual(width, height)
	win.IsBeingManipulated = true
	m.PendingResizes[win.ID] = [2]int{width, height}
}

func (m *OS) adjustTilingNeighborsGeneric(resized *terminal.Window, newX, newY, newWidth, newHeight int, resize resizeOp) (finalX, finalY, finalRight, finalBottom int) {
	oldX := resized.X
	oldY := resized.Y
	oldRight := resized.X + resized.Width
	oldBottom := resized.Y + resized.Height
	newRight := newX + newWidth
	newBottom := newY + newHeight

	if resized.IsFloating {
		return newX, newY, newRight, newBottom
	}

	const minWidth = config.DefaultWindowWidth
	const minHeight = config.DefaultWindowHeight
	minY := m.PaneTop()
	maxY := minY + m.PaneHeight()
	minX := m.PaneLeft()
	maxX := minX + m.PaneWidth()

	gap := m.separatorGap()
	if newRight != oldRight {
		newRight = m.moveVerticalSplit(oldRight, newRight, gap, resized, resize, minWidth, minX, maxX)
	}
	if newX != oldX {
		newX = m.moveVerticalSplit(oldX-gap, newX-gap, gap, resized, resize, minWidth, minX, maxX) + gap
	}
	if newBottom != oldBottom {
		newBottom = m.moveHorizontalSplit(oldBottom, newBottom, gap, resized, resize, minHeight, minY, maxY)
	}
	if newY != oldY {
		newY = m.moveHorizontalSplit(oldY-gap, newY-gap, gap, resized, resize, minHeight, minY, maxY) + gap
	}

	return newX, newY, newRight, newBottom
}

func (m *OS) moveVerticalSplit(old, requested, gap int, resized *terminal.Window, resize resizeOp, minWidth, minX, maxX int) int {
	leftWindows, rightWindows := findWindowsOnVerticalSplitAll(m, old, gap)

	split := m.constrainVerticalSplit(requested, gap, leftWindows, rightWindows, minWidth, minX, maxX)

	for _, win := range leftWindows {
		if win == resized {
			continue
		}
		resize(m, win, split-win.X, win.Height)
		win.MarkPositionDirty()
	}
	for _, win := range rightWindows {
		if win == resized {
			continue
		}
		oldWinRight := win.X + win.Width
		win.X = split + gap
		resize(m, win, oldWinRight-win.X, win.Height)
		win.MarkPositionDirty()
	}
	return split
}

func (m *OS) moveHorizontalSplit(old, requested, gap int, resized *terminal.Window, resize resizeOp, minHeight, minY, maxY int) int {
	topWindows, bottomWindows := findWindowsOnHorizontalSplitAll(m, old, gap)

	split := m.constrainHorizontalSplit(requested, gap, topWindows, bottomWindows, minHeight, minY, maxY)

	for _, win := range topWindows {
		if win == resized {
			continue
		}
		resize(m, win, win.Width, split-win.Y)
		win.MarkPositionDirty()
	}
	for _, win := range bottomWindows {
		if win == resized {
			continue
		}
		oldWinBottom := win.Y + win.Height
		win.Y = split + gap
		resize(m, win, win.Width, oldWinBottom-win.Y)
		win.MarkPositionDirty()
	}
	return split
}

func (m *OS) applyBSPResize(resized *terminal.Window, newX, newY, newWidth, newHeight int, resize resizeOp) bool {
	if !m.AutoTiling || !m.UseBSPLayout || m.UseScrollingLayout || resized.IsFloating {
		return false
	}

	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil || tree.IsEmpty() {
		return false
	}

	intID := m.GetWindowIntID(resized.ID)
	if !tree.HasWindow(intID) {
		return false
	}

	bounds := m.GetBSPBounds()
	moved := false
	for _, edge := range []struct {
		e   layout.ResizeEdge
		old int
		new int
	}{
		{layout.ResizeEdgeRight, resized.X + resized.Width, newX + newWidth},
		{layout.ResizeEdgeLeft, resized.X, newX},
		{layout.ResizeEdgeBottom, resized.Y + resized.Height, newY + newHeight},
		{layout.ResizeEdgeTop, resized.Y, newY},
	} {
		if edge.old == edge.new {
			continue
		}
		if tree.ResizeSplit(intID, edge.e, edge.new, bounds, m.separatorGap()) {
			moved = true
		}
	}
	if !moved {
		return false
	}

	m.pendingBSPSync = false

	if m.bspResizeScratch == nil {
		m.bspResizeScratch = make(map[int]layout.Rect, len(m.Windows))
	}
	for windowIntID, rect := range tree.ApplyLayoutInto(bounds, m.bspResizeScratch, m.separatorGap()) {
		win := m.GetWindowByIntID(windowIntID)
		if win == nil || win.Workspace != m.CurrentWorkspace || win.Minimized || win.IsFloating {
			continue
		}
		m.CancelSnapAnimation(win)

		if win.X == rect.X && win.Y == rect.Y && win.Width == rect.W && win.Height == rect.H {
			continue
		}
		win.X, win.Y = rect.X, rect.Y
		resize(m, win, rect.W, rect.H)
		win.MarkPositionDirty()
	}
	return true
}

func (m *OS) constrainVerticalSplit(requested, gap int, leftWindows, rightWindows []*terminal.Window, minWidth, minX, maxX int) int {
	minValidX := minX
	for _, win := range leftWindows {
		minValidX = max(minValidX, win.X+minWidth)
	}

	maxValidX := maxX - gap
	for _, win := range rightWindows {
		maxValidX = min(maxValidX, win.X+win.Width-minWidth-gap)
	}

	return max(minValidX, min(requested, maxValidX))
}

func (m *OS) constrainHorizontalSplit(requested, gap int, topWindows, bottomWindows []*terminal.Window, minHeight, minY, maxY int) int {
	minValidY := minY
	for _, win := range topWindows {
		minValidY = max(minValidY, win.Y+minHeight)
	}

	maxValidY := maxY - gap
	for _, win := range bottomWindows {
		maxValidY = min(maxValidY, win.Y+win.Height-minHeight-gap)
	}

	return max(minValidY, min(requested, maxValidY))
}

func (m *OS) applyTilingResult(resized *terminal.Window, finalX, finalY, finalRight, finalBottom int) {
	const minWidth = config.DefaultWindowWidth
	const minHeight = config.DefaultWindowHeight
	minY := m.PaneTop()
	maxY := minY + m.PaneHeight()
	minX := m.PaneLeft()
	maxX := minX + m.PaneWidth()

	resized.X = finalX
	resized.Y = finalY
	resized.Width = finalRight - finalX
	resized.Height = finalBottom - finalY

	if resized.Width < minWidth || resized.Height < minHeight ||
		resized.X < minX || resized.Y < 0 ||
		resized.X+resized.Width > maxX || resized.Y+resized.Height > maxY {
		resized.Width = max(minWidth, min(resized.Width, maxX-resized.X))
		resized.Height = max(minHeight, min(resized.Height, maxY-resized.Y))
		resized.X = max(minX, min(resized.X, maxX-minWidth))
		resized.Y = max(minY, min(resized.Y, maxY-minHeight))
	}
}

// AdjustTilingNeighbors adjusts ALL windows on affected split lines with constraint-based positioning.
// This is the core tiling resize algorithm used by both mouse and keyboard resize operations.
func (m *OS) AdjustTilingNeighbors(resized *terminal.Window, newX, newY, newWidth, newHeight int) {
	if !m.applyBSPResize(resized, newX, newY, newWidth, newHeight, resizeImmediate) {
		finalX, finalY, finalRight, finalBottom := m.adjustTilingNeighborsGeneric(resized, newX, newY, newWidth, newHeight, resizeImmediate)
		m.applyTilingResult(resized, finalX, finalY, finalRight, finalBottom)

		resized.Resize(resized.Width, resized.Height)
		resized.MarkPositionDirty()
		m.SyncMasterStackFromGeometry()
	}
	m.MarkLayoutCustom()

	m.FireResized(resized)
}

// AdjustTilingNeighborsVisual is like AdjustTilingNeighbors but uses visual-only resize.
// This defers PTY resize operations until the drag completes, improving responsiveness
// during mouse resize operations while still constraining window sizes appropriately.
//
// It reports whether the BSP tree already describes the resulting geometry. When
// it does, the caller must not ask for a ratio sync: the tree led and the
// windows followed, so re-deriving ratios from geometry would be work at best
// and would undo the divider that was just moved at worst.
func (m *OS) AdjustTilingNeighborsVisual(resized *terminal.Window, newX, newY, newWidth, newHeight int) (treeInSync bool) {
	if m.applyBSPResize(resized, newX, newY, newWidth, newHeight, resizeVisual) {
		return true
	}

	finalX, finalY, finalRight, finalBottom := m.adjustTilingNeighborsGeneric(resized, newX, newY, newWidth, newHeight, resizeVisual)
	m.applyTilingResult(resized, finalX, finalY, finalRight, finalBottom)

	resized.ResizeVisual(resized.Width, resized.Height)
	m.PendingResizes[resized.ID] = [2]int{resized.Width, resized.Height}
	resized.MarkPositionDirty()
	return false
}

// SyncMasterStackFromGeometry writes the split the master-stack panes now show
// back into the ratios the tiler lays them out from, so a resize survives the
// next retile.
//
// A master-stack resize goes through the geometry scan, which moves the pane
// rectangles and nothing else. The tiler keeps no tree: it recomputes every
// rectangle from MasterRatio and the stack ratio, so without this the next
// retile would put the split back where it was. applyBSPResize avoids the same
// loss by writing into the tree first; this is the master-stack equivalent, run
// after the fact because the layout has only ratios to write into.
//
// It reads the ratios back with layout.MasterRatiosFrom, the inverse of the
// tiler, in every master position and with any master count: the master ratio
// is the masters' share along the axis between them and the stack, and the
// stack ratio is the first stack pane's share when the stack holds exactly two
// panes in one column or row. Every other split (a third stack pane, a second
// master, the rows and cells of the default grid) is read with
// layout.MasterSplitsFrom into WorkspaceMasterSplits. Geometry that is not the
// shape the tiler would draw (a zoom, or panes out of their slots) is left
// alone rather than read as a ratio.
//
// With the masters in the centre a resize moves one side's divider, so the
// stack on that side ends up wider than the other. The ratio records the
// masters' new width, and a retile puts them back in the middle.
//
// The keyboard path calls this on every press. The mouse path calls it once,
// on release, before the layout is marked custom and the state is pushed.
func (m *OS) SyncMasterStackFromGeometry() {
	if !m.AutoTiling || m.UseBSPLayout || m.UseScrollingLayout || m.zoomedWindow() != nil {
		return
	}
	region := layout.Rect{X: m.PaneLeft(), Y: m.PaneTop(), W: m.PaneWidth(), H: m.PaneHeight()}
	panes := m.tilablePanes(m.CurrentWorkspace)
	rects := make([]layout.Rect, len(panes))
	for i, w := range panes {
		rects[i] = layout.Rect{X: w.X, Y: w.Y, W: w.Width, H: w.Height}
	}
	p := m.masterParams()
	if splits, ok := layout.MasterSplitsFrom(rects, region, p); ok {
		m.setWorkspaceMasterSplits(m.CurrentWorkspace, splits)
	}
	ratio, stackRatio, ok := layout.MasterRatiosFrom(rects, region, p)
	if !ok {
		return
	}
	m.setMasterRatio(ratio)
	if stackRatio > 0 {
		m.setWorkspaceStackRatio(m.CurrentWorkspace, stackRatio)
	}
	if p.Position == config.MasterPositionCenter {
		m.TileAllWindows()
	}
}

func (m *OS) setWorkspaceMasterSplits(workspace int, splits layout.MasterSplits) {
	if m.WorkspaceMasterSplits == nil {
		m.WorkspaceMasterSplits = make(map[int]layout.MasterSplits)
	}
	m.WorkspaceMasterSplits[workspace] = splits
}

func (m *OS) setMasterRatio(ratio float64) {
	m.MasterRatio = ratio
	if m.WorkspaceMasterRatio == nil {
		m.WorkspaceMasterRatio = make(map[int]float64)
	}
	m.WorkspaceMasterRatio[m.CurrentWorkspace] = ratio
}

func (m *OS) setWorkspaceStackRatio(workspace int, ratio float64) {
	if m.WorkspaceStackRatio == nil {
		m.WorkspaceStackRatio = make(map[int]float64)
	}
	m.WorkspaceStackRatio[workspace] = ratio
}

func splitTolerance(gap int) int {
	if gap == 0 {
		return 1
	}
	return 0
}

func findWindowsOnVerticalSplitAll(m *OS, splitX, gap int) (leftWindows, rightWindows []*terminal.Window) {
	tolerance := splitTolerance(gap)
	for _, win := range m.Windows {
		if win.Workspace != m.CurrentWorkspace || win.Minimized || win.IsFloating {
			continue
		}
		if abs(win.X+win.Width-splitX) <= tolerance {
			leftWindows = append(leftWindows, win)
		} else if abs(win.X-(splitX+gap)) <= tolerance {
			rightWindows = append(rightWindows, win)
		}
	}
	return leftWindows, rightWindows
}

func findWindowsOnHorizontalSplitAll(m *OS, splitY, gap int) (topWindows, bottomWindows []*terminal.Window) {
	tolerance := splitTolerance(gap)
	for _, win := range m.Windows {
		if win.Workspace != m.CurrentWorkspace || win.Minimized || win.IsFloating {
			continue
		}
		if abs(win.Y+win.Height-splitY) <= tolerance {
			topWindows = append(topWindows, win)
		} else if abs(win.Y-(splitY+gap)) <= tolerance {
			bottomWindows = append(bottomWindows, win)
		}
	}
	return topWindows, bottomWindows
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ResizeWindowByID moves one border of a tiled pane by a share of the pane
// region, as herdr's pane.resize does, and reports whether the pane's
// rectangle changed. dir names the way the border moves: right and down move
// it right or down, left and up move it left or up. The border is the one on
// that side of the pane, or the one on the other side when the pane is at the
// region's edge there. So "right" grows a pane that has a neighbour on its
// right, and shrinks one at the right edge from its left. amount is the share
// of the region's width or height, at least one cell. The focus does not
// move. A floating layout, a scrolling layout, or a pane on a workspace that
// is not shown has no border to move: nothing changes.
func (m *OS) ResizeWindowByID(id, dir string, amount float64) (bool, error) {
	idx := m.windowIndexByID(id)
	if idx < 0 {
		return false, errfmt.Errorf("no pane %s to resize", id)
	}
	w := m.Windows[idx]
	if !m.AutoTiling || m.UseScrollingLayout || w.Workspace != m.CurrentWorkspace || w.Minimized || w.IsPopup {
		return false, nil
	}
	m.landSnapAnimations()
	m.CancelAnimationsForWindow(w)
	horizontal := dir == "left" || dir == "right"
	span := m.PaneHeight()
	if horizontal {
		span = m.PaneWidth()
	}
	delta := max(int(math.Round(amount*float64(span))), 1)
	if dir == "left" || dir == "up" {
		delta = -delta
	}
	x, y, width, height := w.X, w.Y, w.Width, w.Height
	atLeft := x <= m.PaneLeft()+edgeTolerance
	atRight := x+width >= m.PaneLeft()+m.PaneWidth()-edgeTolerance
	atTop := y <= m.PaneTop()+edgeTolerance
	atBottom := y+height >= m.PaneTop()+m.PaneHeight()-edgeTolerance
	near, far := atRight, atLeft
	if dir == "left" {
		near, far = atLeft, atRight
	}
	if !horizontal {
		near, far = atBottom, atTop
		if dir == "up" {
			near, far = atTop, atBottom
		}
	}
	moveFar := near
	if near && far {
		return false, nil
	}
	nx, ny, nw, nh := x, y, width, height
	switch {
	case horizontal && (dir == "right") != moveFar:
		nw = width + delta
	case horizontal:
		nx, nw = x+delta, width-delta
	case (dir == "down") != moveFar:
		nh = height + delta
	default:
		ny, nh = y+delta, height-delta
	}
	if nw < 1 || nh < 1 {
		return false, nil
	}
	m.AdjustTilingNeighbors(w, nx, ny, nw, nh)
	return w.X != x || w.Y != y || w.Width != width || w.Height != height, nil
}
