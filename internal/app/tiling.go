package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// tileLayout is a private type for compatibility with existing code
type tileLayout struct {
	x, y, width, height int
}

// calculateTilingLayout is a wrapper around layout.CalculateTilingLayout for internal use
func (m *OS) calculateTilingLayout(n int) []tileLayout {
	layouts := layout.CalculateTilingLayout(n, m.Width, m.GetUsableHeight())
	result := make([]tileLayout, len(layouts))
	for i, l := range layouts {
		result[i] = tileLayout{
			x:      l.X,
			y:      l.Y,
			width:  l.Width,
			height: l.Height,
		}
	}
	return result
}

// TileAllWindows arranges all visible windows in a tiling layout
func (m *OS) TileAllWindows() {
	// Get list of visible windows in current workspace (not minimized)
	var visibleWindows []*terminal.Window
	var visibleIndices []int
	for i, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing {
			visibleWindows = append(visibleWindows, w)
			visibleIndices = append(visibleIndices, i)
		}
	}

	if len(visibleWindows) == 0 {
		return
	}

	// Calculate tiling layout based on number of windows
	layouts := layout.CalculateTilingLayout(len(visibleWindows), m.Width, m.GetUsableHeight())

	// Apply layout with animations
	for i, idx := range visibleIndices {
		if i >= len(layouts) {
			break
		}

		l := layouts[i]

		// Create animation for smooth transition
		anim := ui.NewSnapAnimation(
			m.Windows[idx],
			l.X, l.Y, l.Width, l.Height,
			config.DefaultAnimationDuration,
		)

		if anim != nil {
			m.Animations = append(m.Animations, anim)
		}
	}
}

// ToggleAutoTiling toggles automatic tiling mode
func (m *OS) ToggleAutoTiling() {
	m.AutoTiling = !m.AutoTiling

	if m.AutoTiling {
		// When enabling, tile all existing windows
		m.TileAllWindows()
	}
}

// TileNewWindow arranges the new window in the tiling layout
func (m *OS) TileNewWindow() {
	if !m.AutoTiling {
		return
	}

	// Retile all windows including the new one
	m.TileAllWindows()
}

// RetileAfterClose handles window close in tiling mode
func (m *OS) RetileAfterClose() {
	if !m.AutoTiling {
		return
	}

	// Retile remaining windows
	m.TileAllWindows()
}

// SwapWindows swaps the positions of two windows with animation
func (m *OS) SwapWindows(index1, index2 int) {
	if index1 < 0 || index1 >= len(m.Windows) || index2 < 0 || index2 >= len(m.Windows) {
		return
	}

	window1 := m.Windows[index1]
	window2 := m.Windows[index2]

	// Store the positions for swapping
	x1, y1, width1, height1 := window1.X, window1.Y, window1.Width, window1.Height
	x2, y2, width2, height2 := window2.X, window2.Y, window2.Width, window2.Height

	// Create animations for both windows to swap positions
	anim1 := ui.NewSnapAnimation(
		window1,
		x2, y2, width2, height2,
		config.FastAnimationDuration,
	)

	anim2 := ui.NewSnapAnimation(
		window2,
		x1, y1, width1, height1,
		config.FastAnimationDuration,
	)

	m.Animations = append(m.Animations, anim1, anim2)
}

// SwapWindowsInstant swaps the positions of two windows instantly without animation
func (m *OS) SwapWindowsInstant(index1, index2 int) {
	if index1 < 0 || index1 >= len(m.Windows) || index2 < 0 || index2 >= len(m.Windows) {
		return
	}

	window1 := m.Windows[index1]
	window2 := m.Windows[index2]

	// Store the positions for swapping
	x1, y1, w1, h1 := window1.X, window1.Y, window1.Width, window1.Height
	x2, y2, w2, h2 := window2.X, window2.Y, window2.Width, window2.Height

	// Swap positions instantly
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
}

// SwapWindowsWithOriginal swaps windows where the dragged window's original position is provided
func (m *OS) SwapWindowsWithOriginal(draggedIndex, targetIndex int, origX, origY, origWidth, origHeight int) {
	if draggedIndex < 0 || draggedIndex >= len(m.Windows) || targetIndex < 0 || targetIndex >= len(m.Windows) {
		return
	}

	draggedWindow := m.Windows[draggedIndex]
	targetWindow := m.Windows[targetIndex]

	// Dragged window goes to target's position
	anim1 := ui.NewSnapAnimation(
		draggedWindow,
		targetWindow.X, targetWindow.Y, targetWindow.Width, targetWindow.Height,
		config.FastAnimationDuration,
	)

	// Target window goes to dragged window's ORIGINAL position
	anim2 := ui.NewSnapAnimation(
		targetWindow,
		origX, origY, origWidth, origHeight,
		config.FastAnimationDuration,
	)

	if anim1 != nil {
		m.Animations = append(m.Animations, anim1)
	}
	if anim2 != nil {
		m.Animations = append(m.Animations, anim2)
	}
}

// TileRemainingWindows tiles all windows except the one being minimized
func (m *OS) TileRemainingWindows(excludeIndex int) {
	// Get list of visible windows (not minimized and not the one being minimized)
	var visibleWindows []*terminal.Window
	var visibleIndices []int
	for i, w := range m.Windows {
		if i != excludeIndex && !w.Minimized && !w.Minimizing {
			visibleWindows = append(visibleWindows, w)
			visibleIndices = append(visibleIndices, i)
		}
	}

	if len(visibleWindows) == 0 {
		return
	}

	// Calculate tiling layout based on number of remaining windows
	layouts := layout.CalculateTilingLayout(len(visibleWindows), m.Width, m.GetUsableHeight())

	// Apply layout with animations
	for i, idx := range visibleIndices {
		if i >= len(layouts) {
			break
		}

		l := layouts[i]

		// Create animation for smooth transition
		anim := ui.NewSnapAnimation(
			m.Windows[idx],
			l.X, l.Y, l.Width, l.Height,
			config.DefaultAnimationDuration,
		)

		if anim != nil {
			m.Animations = append(m.Animations, anim)
		}
	}
}

// SwapWindowLeft swaps the focused window with the window to its left
func (o *OS) SwapWindowLeft() {
	if o.FocusedWindow < 0 || o.FocusedWindow >= len(o.Windows) {
		return
	}

	// Don't swap if animations are in progress
	if o.HasActiveAnimations() {
		return
	}

	focusedWindow := o.Windows[o.FocusedWindow]

	// Find the window to the left
	targetIndex := -1
	minDistance := o.Width

	for i, window := range o.Windows {
		if i == o.FocusedWindow || window.Minimized || window.Minimizing {
			continue
		}

		// Check if window is to the left
		if window.X+window.Width <= focusedWindow.X+5 {
			// Check if it overlaps vertically
			if window.Y < focusedWindow.Y+focusedWindow.Height &&
				window.Y+window.Height > focusedWindow.Y {
				// Find the closest one
				distance := focusedWindow.X - (window.X + window.Width)
				if distance < minDistance {
					minDistance = distance
					targetIndex = i
				}
			}
		}
	}

	if targetIndex >= 0 {
		// Swap instantly without animation for keyboard shortcuts
		o.SwapWindowsInstant(o.FocusedWindow, targetIndex)
	}
}

// SwapWindowRight swaps the focused window with the window to its right
func (o *OS) SwapWindowRight() {
	if o.FocusedWindow < 0 || o.FocusedWindow >= len(o.Windows) {
		return
	}

	// Don't swap if animations are in progress
	if o.HasActiveAnimations() {
		return
	}

	focusedWindow := o.Windows[o.FocusedWindow]

	// Find the window to the right
	targetIndex := -1
	minDistance := o.Width

	for i, window := range o.Windows {
		if i == o.FocusedWindow || window.Minimized || window.Minimizing {
			continue
		}

		// Check if window is to the right
		if window.X >= focusedWindow.X+focusedWindow.Width-5 {
			// Check if it overlaps vertically
			if window.Y < focusedWindow.Y+focusedWindow.Height &&
				window.Y+window.Height > focusedWindow.Y {
				// Find the closest one
				distance := window.X - (focusedWindow.X + focusedWindow.Width)
				if distance < minDistance {
					minDistance = distance
					targetIndex = i
				}
			}
		}
	}

	if targetIndex >= 0 {
		// Swap instantly without animation for keyboard shortcuts
		o.SwapWindowsInstant(o.FocusedWindow, targetIndex)
	}
}

// SwapWindowUp swaps the focused window with the window above it
func (o *OS) SwapWindowUp() {
	if o.FocusedWindow < 0 || o.FocusedWindow >= len(o.Windows) {
		return
	}

	// Don't swap if animations are in progress
	if o.HasActiveAnimations() {
		return
	}

	focusedWindow := o.Windows[o.FocusedWindow]

	// Find the window above
	targetIndex := -1
	minDistance := o.Height

	for i, window := range o.Windows {
		if i == o.FocusedWindow || window.Minimized || window.Minimizing {
			continue
		}

		// Check if window is above
		if window.Y+window.Height <= focusedWindow.Y+5 {
			// Check if it overlaps horizontally
			if window.X < focusedWindow.X+focusedWindow.Width &&
				window.X+window.Width > focusedWindow.X {
				// Find the closest one
				distance := focusedWindow.Y - (window.Y + window.Height)
				if distance < minDistance {
					minDistance = distance
					targetIndex = i
				}
			}
		}
	}

	if targetIndex >= 0 {
		// Swap instantly without animation for keyboard shortcuts
		o.SwapWindowsInstant(o.FocusedWindow, targetIndex)
	}
}

// SwapWindowDown swaps the focused window with the window below it
func (o *OS) SwapWindowDown() {
	if o.FocusedWindow < 0 || o.FocusedWindow >= len(o.Windows) {
		return
	}

	// Don't swap if animations are in progress
	if o.HasActiveAnimations() {
		return
	}

	focusedWindow := o.Windows[o.FocusedWindow]

	// Find the window below
	targetIndex := -1
	minDistance := o.Height

	for i, window := range o.Windows {
		if i == o.FocusedWindow || window.Minimized || window.Minimizing {
			continue
		}

		// Check if window is below
		if window.Y >= focusedWindow.Y+focusedWindow.Height-5 {
			// Check if it overlaps horizontally
			if window.X < focusedWindow.X+focusedWindow.Width &&
				window.X+window.Width > focusedWindow.X {
				// Find the closest one
				distance := window.Y - (focusedWindow.Y + focusedWindow.Height)
				if distance < minDistance {
					minDistance = distance
					targetIndex = i
				}
			}
		}
	}

	if targetIndex >= 0 {
		// Swap instantly without animation for keyboard shortcuts
		o.SwapWindowsInstant(o.FocusedWindow, targetIndex)
	}
}
