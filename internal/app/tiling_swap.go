package app

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// SwapWindowsInstant swaps the positions of two windows instantly without animation
func (m *OS) SwapWindowsInstant(index1, index2 int) {
	m.swapWindows(index1, index2, 0)
}

func (m *OS) destinationOf(w *terminal.Window) (x, y, width, height int) {
	if a := m.snapInFlight(w); a != nil {
		return a.EndX, a.EndY, a.EndWidth, a.EndHeight
	}
	return w.X, w.Y, w.Width, w.Height
}

func (m *OS) swapWindows(index1, index2 int, dur time.Duration) {
	if index1 < 0 || index1 >= len(m.Windows) || index2 < 0 || index2 >= len(m.Windows) {
		return
	}

	window1 := m.Windows[index1]
	window2 := m.Windows[index2]

	x1, y1, w1, h1 := m.destinationOf(window1)
	x2, y2, w2, h2 := m.destinationOf(window2)

	if dur > 0 {
		m.animateWindow(window1, x2, y2, w2, h2, dur)
		m.animateWindow(window2, x1, y1, w1, h1, dur)
		m.finishSwap(index1, index2)
		return
	}
	m.CancelSnapAnimation(window1)
	m.CancelSnapAnimation(window2)

	window1.X = x2
	window1.Y = y2
	window1.Width = w2
	window1.Height = h2
	window1.Resize(w2, h2)
	window1.MarkPositionDirty()
	window1.InvalidateCache()

	window2.X = x1
	window2.Y = y1
	window2.Width = w1
	window2.Height = h1
	window2.Resize(w1, h1)
	window2.MarkPositionDirty()
	window2.InvalidateCache()

	m.finishSwap(index1, index2)
}

func (m *OS) finishSwap(index1, index2 int) {
	window1, window2 := m.Windows[index1], m.Windows[index2]
	m.SwapWindowsInBSPTree(window1, window2)

	m.Windows[index1], m.Windows[index2] = m.Windows[index2], m.Windows[index1]

	switch m.FocusedWindow {
	case index1:
		m.FocusedWindow = index2
	case index2:
		m.FocusedWindow = index1
	}

	m.SyncStateToDaemon()
}

// SwapWindowsWithOriginal swaps windows where the dragged window's original position is provided
func (m *OS) SwapWindowsWithOriginal(draggedIndex, targetIndex int, origX, origY, origWidth, origHeight int) {
	if draggedIndex < 0 || draggedIndex >= len(m.Windows) || targetIndex < 0 || targetIndex >= len(m.Windows) {
		return
	}

	draggedWindow := m.Windows[draggedIndex]
	targetWindow := m.Windows[targetIndex]

	targetX, targetY, targetW, targetH := targetWindow.X, targetWindow.Y, targetWindow.Width, targetWindow.Height

	m.SwapWindowsInBSPTree(draggedWindow, targetWindow)

	m.Windows[draggedIndex], m.Windows[targetIndex] = m.Windows[targetIndex], m.Windows[draggedIndex]

	switch m.FocusedWindow {
	case draggedIndex:
		m.FocusedWindow = targetIndex
	case targetIndex:
		m.FocusedWindow = draggedIndex
	}

	anim1 := ui.NewSnapAnimation(
		draggedWindow,
		targetX, targetY, targetW, targetH,
		m.Settings.GetFastAnimationDuration(),
	)

	anim2 := ui.NewSnapAnimation(
		targetWindow,
		origX, origY, origWidth, origHeight,
		m.Settings.GetFastAnimationDuration(),
	)

	if anim1 != nil {
		m.addAnimation(anim1)
	}
	if anim2 != nil {
		m.addAnimation(anim2)
	}

	m.SyncStateToDaemon()
}

// TileRemainingWindows tiles all windows except the one being minimized
func (m *OS) TileRemainingWindows(excludeIndex int) {
	var visibleWindows []*terminal.Window
	var visibleIndices []int
	for i, w := range m.Windows {
		if i != excludeIndex && w.Workspace == m.CurrentWorkspace && !w.Minimized {
			visibleWindows = append(visibleWindows, w)
			visibleIndices = append(visibleIndices, i)
		}
	}

	if len(visibleWindows) == 0 {
		return
	}

	layouts := m.contentTileLayouts(len(visibleWindows))

	for i, idx := range visibleIndices {
		if i >= len(layouts) {
			break
		}

		l := layouts[i]

		anim := ui.NewSnapAnimation(
			m.Windows[idx],
			l.X, l.Y, l.Width, l.Height,
			m.Settings.GetAnimationDuration(),
		)

		if anim != nil {
			m.addAnimation(anim)
		}
	}
}

// SwapWindow swaps the focused window with the adjacent window in the given direction
func (m *OS) SwapWindow(dir Direction) {
	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}

	focusedWindow := m.Windows[m.FocusedWindow]

	if focusedWindow.IsFloating {
		return
	}
	targetIndex := m.findAdjacentWindow(focusedWindow, dir)

	if targetIndex >= 0 {
		m.swapWindows(m.FocusedWindow, targetIndex, m.Settings.GetFastAnimationDuration())
	}
}

func (m *OS) findAdjacentWindow(focused *terminal.Window, dir Direction) int {
	fx, fy, fw, fh := m.destinationOf(focused)
	targetIndex := -1
	var minDistance int

	if dir == DirLeft || dir == DirRight {
		minDistance = m.Width
	} else {
		minDistance = m.Height
	}

	for i, window := range m.Windows {
		if i == m.FocusedWindow || window.Workspace != m.CurrentWorkspace || window.Minimized || window.IsFloating {
			continue
		}

		wx, wy, ww, wh := m.destinationOf(window)
		var isInDirection, overlaps bool
		var distance int

		switch dir {
		case DirLeft:
			isInDirection = wx+ww <= fx+swapTolerance
			overlaps = wy < fy+fh && wy+wh > fy
			distance = fx - (wx + ww)
		case DirRight:
			isInDirection = wx >= fx+fw-swapTolerance
			overlaps = wy < fy+fh && wy+wh > fy
			distance = wx - (fx + fw)
		case DirUp:
			isInDirection = wy+wh <= fy+swapTolerance
			overlaps = wx < fx+fw && wx+ww > fx
			distance = fy - (wy + wh)
		case DirDown:
			isInDirection = wy >= fy+fh-swapTolerance
			overlaps = wx < fx+fw && wx+ww > fx
			distance = wy - (fy + fh)
		}

		if isInDirection && overlaps && distance < minDistance {
			minDistance = distance
			targetIndex = i
		}
	}

	return targetIndex
}

// SwapWindowLeft swaps the focused window with the window to its left
func (m *OS) SwapWindowLeft() {
	m.SwapWindow(DirLeft)
}

// SwapWindowRight swaps the focused window with the window to its right
func (m *OS) SwapWindowRight() {
	m.SwapWindow(DirRight)
}

// SwapWindowUp swaps the focused window with the window above it
func (m *OS) SwapWindowUp() {
	m.SwapWindow(DirUp)
}

// SwapWindowDown swaps the focused window with the window below it
func (m *OS) SwapWindowDown() {
	m.SwapWindow(DirDown)
}

// SwapWindowsByID swaps two panes by window id and leaves the focus on the
// first, at the place the second held. It is herdr's pane.swap, which the
// daemon routes here because the split tree lives in the client: the daemon
// picks the pair, and the client moves them.
func (m *OS) SwapWindowsByID(source, target string) error {
	si, ti := m.windowIndexByID(source), m.windowIndexByID(target)
	if si < 0 {
		return errfmt.Errorf("no pane %s to swap", source)
	}
	if ti < 0 {
		return errfmt.Errorf("no pane %s to swap", target)
	}
	if si == ti {
		return nil
	}
	m.swapWindows(si, ti, m.Settings.GetFastAnimationDuration())
	m.FocusWindow(ti)
	return nil
}
